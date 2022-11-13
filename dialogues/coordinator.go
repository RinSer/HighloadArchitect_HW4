package dialogues

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-redis/redis/v9"
	proxysql "github.com/kirinrastogi/proxysql-go"
	"github.com/labstack/echo"
)

const (
	UserDataHostGroupId   = 1
	DefaultHostsGroupId   = 2
	DedicatedHostsGroupId = 3
)

type Coordinator struct {
	ctx            context.Context
	rdb            *redis.Client
	conn           *proxysql.ProxySQL
	userHost       *sql.DB
	hosts          map[int64]*sql.DB
	dedicatedHosts map[int64]*sql.DB
}

func NewCoordinator(proxySqlConnection string, redisHost string) Coordinator {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// connect to redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisHost,
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	conn, err := proxysql.NewProxySQL(proxySqlConnection)
	if err != nil {
		log.Fatal(err)
	}

	err = conn.Clear()
	if err != nil {
		log.Fatal(err)
	}

	dc := Coordinator{
		ctx:  ctx,
		rdb:  rdb,
		conn: conn,
	}
	dc.initHosts()

	return dc
}

// API handlers

func (dc *Coordinator) AddUser(c echo.Context) (err error) {
	u := new(User)
	err = c.Bind(u)
	if err != nil {
		return
	}
	tx, err := dc.userHost.BeginTx(dc.ctx, nil)
	defer func() {
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}()
	if err != nil {
		return
	}
	_, err = tx.Exec(`INSERT INTO users (login) values (?)`)
	if err != nil {
		return
	}
	row := tx.QueryRow(`SELECT LAST_INSERT_ID()`)
	row.Scan(&u.Id)
	return c.JSON(http.StatusCreated, u.Id)
}

func (dc *Coordinator) AddMessage(c echo.Context) (err error) {
	msg := new(Message)
	err = c.Bind(msg)
	if err != nil {
		return
	}
	host := dc.getUserHost(msg.From)
	_, err = host.Exec(`
	INSERT INTO messages (from, to, text, at) VALUES (?, ?, ?, ?)`,
		msg.From, msg.To, msg.Text, time.Now())
	go dc.updateHosts(*msg)
	return
}

func (dc *Coordinator) GetDialogue(c echo.Context) (err error) {
	userId1, err := strconv.ParseInt(c.QueryParam("user1"), 10, 64)
	if err != nil {
		return
	}
	userId2, err := strconv.ParseInt(c.QueryParam("user2"), 10, 64)
	if err != nil {
		return
	}
	msgs1, err := dc.getUserMessages(userId1, userId2)
	if err != nil {
		return
	}
	msgs2, err := dc.getUserMessages(userId2, userId1)
	if err != nil {
		return
	}
	return c.JSON(http.StatusOK, append(msgs1, msgs2...))
}

// Helpers

func (dc *Coordinator) getUserMessages(userId1 int64, userId2 int64) ([]Message, error) {
	host := dc.getUserHost(userId1)
	rows, err := host.Query(
		`SELECT from, to, text, at FROM queries WHERE from = ? and to = ?`,
		userId1, userId2)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs := make([]Message, 0)
	for rows.Next() {
		msg := Message{}
		err := rows.Scan(&msg.From, &msg.To, &msg.Text, &msg.At)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func (dc *Coordinator) getUserHost(userId int64) *sql.DB {
	if host, ok := dc.dedicatedHosts[userId]; ok {
		return host
	}
	hostId := userId % int64(len(dc.hosts))
	return dc.hosts[hostId]
}

func (dc *Coordinator) updateHosts(msg Message) {
	userKey := strconv.FormatInt(msg.From, 10)
	currentUserLoad := 0
	currentUserLoadValue, err := dc.rdb.Get(dc.ctx, userKey).Result()
	if err == nil {
		currentUserLoad, _ = strconv.Atoi(currentUserLoadValue)
	}
	currentUserLoad += len(msg.Text)
	_ = dc.rdb.Set(dc.ctx, userKey, strconv.Itoa(currentUserLoad), 0)
	// add dedicated host for a user if necessary
	if currentUserLoad > 1_000_000 { // > 1Mb
		// load all the existing messages
		currentHost := dc.getUserHost(msg.From)
		rows, err := currentHost.Query(
			`SELECT from, to, text, at FROM messages WHERE from = ?`, msg.From)
		if err != nil {
			log.Print(err)
			return
		}
		defer rows.Close()
		msgs := make([]Message, 0)
		for rows.Next() {
			msg := Message{}
			err = rows.Scan(&msg.From, &msg.To, &msg.Text, &msg.At)
			if err != nil {
				log.Print(err)
				return
			}
			msgs = append(msgs, msg)
		}
		// add new host for a user
		hostName := strconv.FormatInt(msg.From, 10)
		err = dc.conn.AddHost(proxysql.Hostname(hostName),
			proxysql.HostgroupID(DedicatedHostsGroupId))
		if err != nil {
			log.Print(err)
			return
		}
		err = dc.conn.PersistChanges()
		if err != nil {
			log.Print(err)
			return
		}
		hosts, err := dc.conn.All()
		if err != nil {
			log.Print(err)
			return
		}
		for _, host := range hosts {
			if host.HostgroupID() == DedicatedHostsGroupId &&
				host.Hostname() == hostName {
				dc.dedicatedHosts[msg.From], err = connectToHost(host.Port())
				if err != nil {
					log.Print(err)
				}
				_, err = dc.dedicatedHosts[msg.From].Exec(`
				CREATE TABLE IF NOT EXISTS messages (
					from BIGINT,
					to   BIGINT,
					text TEXT,
					at   TIMESTAMP,
					PRIMARY KEY(from, to, at)
				)`)
				if err != nil {
					log.Print(err)
					return
				}
				query := `INSERT INTO messages (from, to, text, at) VALUES`
				vals := make([]interface{}, 0)
				for _, msg := range msgs {
					query += " (?, ?, ?, ?)"
					vals = append(vals, msg.From, msg.To, msg.Text, msg.At)
				}
				stmt, err := dc.dedicatedHosts[msg.From].Prepare(query)
				if err != nil {
					log.Print(err)
					return
				}
				defer stmt.Close()
				_, err = stmt.Exec(vals...)
				if err != nil {
					log.Print(err)
				}
				return
			}
		}
	}
}

func (dc *Coordinator) initHosts() (err error) {
	_, err = dc.conn.Conn().Exec(`CREATE DATABASE IF NOT EXISTS dialogues`)
	if err != nil {
		return
	}
	dc.hosts = make(map[int64]*sql.DB)
	dc.dedicatedHosts = make(map[int64]*sql.DB)
	hosts, err := dc.conn.All()
	if err != nil {
		return
	}
	if len(hosts) < 2 {
		err = dc.conn.AddHost(proxysql.Hostname("users"),
			proxysql.HostgroupID(UserDataHostGroupId))
		if err != nil {
			return
		}
		for i := 1; i < 4; i++ {
			err = dc.conn.AddHost(proxysql.Hostname(strconv.Itoa(i)),
				proxysql.HostgroupID(DedicatedHostsGroupId))
			if err != nil {
				return
			}
		}
		err = dc.conn.PersistChanges()
		if err != nil {
			return
		}
		hosts, err = dc.conn.All()
		if err != nil {
			return
		}
	}
	var hostId int64
	for _, host := range hosts {
		hostId, err = strconv.ParseInt(host.Hostname(), 10, 64)
		if err != nil {
			return
		}
		switch host.HostgroupID() {
		case DefaultHostsGroupId:
			dc.hosts[hostId], err = connectToHost(host.Port())
			if err != nil {
				return
			}
			_, err = dc.hosts[hostId].Exec(`
			CREATE TABLE IF NOT EXISTS messages (
				from BIGINT,
				to   BIGINT,
				text TEXT,
				at   TIMESTAMP,
				PRIMARY KEY(from, to, at)
			)`)
		case DedicatedHostsGroupId:
			dc.dedicatedHosts[hostId], err = connectToHost(host.Port())
		case UserDataHostGroupId:
			dc.userHost, err = connectToHost(host.Port())
			if err != nil {
				return
			}
			_, err = dc.hosts[hostId].Exec(`
			CREATE TABLE IF NOT EXISTS users (
				id    BIGINT AUTO_INCREMENT PRIMARY KEY,
				login VARCHAR(25)
			)`)
		}
		if err != nil {
			return
		}
	}
	return
}

func connectToHost(port int) (*sql.DB, error) {
	return sql.Open("mysql",
		"client:password@localhost:"+strconv.Itoa(port)+"/dialogues")
}
