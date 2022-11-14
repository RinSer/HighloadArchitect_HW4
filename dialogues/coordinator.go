package dialogues

import (
	"context"
	"database/sql"
	"fmt"
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
	hosts          *sql.DB
	dedicatedHosts *sql.DB
	dedicatedUsers map[int64]bool
}

func NewCoordinator(proxySqlConnection string, redisHost string) (*Coordinator, error) {
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
		return nil, err
	}

	err = conn.Clear()
	if err != nil {
		return nil, err
	}

	dc := &Coordinator{
		ctx:  ctx,
		rdb:  rdb,
		conn: conn,
	}
	err = dc.initHosts()
	if err != nil {
		return nil, err
	}

	return dc, nil
}

// API handlers

func (dc *Coordinator) AddUser(c echo.Context) (err error) {
	u := new(User)
	err = c.Bind(u)
	if err != nil {
		return
	}
	tx, err := dc.hosts.BeginTx(dc.ctx, nil)
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
	_, err = tx.Exec(`INSERT INTO users (login) values (?);`)
	if err != nil {
		return
	}
	row := tx.QueryRow(`SELECT LAST_INSERT_ID();`)
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
	INSERT INTO messages (from, to, txt, at) VALUES (?, ?, ?, ?);`,
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
		`SELECT from, to, txt, at FROM queries WHERE from = ? and to = ?;`,
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
	if _, ok := dc.dedicatedUsers[userId]; ok {
		return dc.dedicatedHosts
	}
	return dc.hosts
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
	// add user to dedicated hosts if necessary
	if currentUserLoad > 1_000_000 { // > 1Mb
		dc.dedicatedUsers[msg.From] = true
	}
}

func (dc *Coordinator) initHosts() (err error) {
	dc.hosts, err = connectToHost("test1", "test1")
	if err != nil {
		return
	}
	_, err = dc.hosts.Exec(`
	CREATE TABLE IF NOT EXISTS users (
		id    BIGINT AUTO_INCREMENT PRIMARY KEY,
		login VARCHAR(25)
	);`)
	if err != nil {
		return
	}
	_, err = dc.hosts.Exec(`
	CREATE TABLE IF NOT EXISTS messages (
		from BIGINT,
		to   BIGINT,
		txt  TEXT,
		at   TIMESTAMP,
		PRIMARY KEY(from, to, at)
	);`)
	if err != nil {
		return
	}
	dc.dedicatedHosts, err = connectToHost("test2", "test2")
	if err != nil {
		return
	}
	_, err = dc.hosts.Exec(`
	CREATE TABLE IF NOT EXISTS messages (
		from BIGINT,
		to   BIGINT,
		text TEXT,
		at   TIMESTAMP,
		PRIMARY KEY(from, to, at)
	);`)
	if err != nil {
		return
	}
	// initialize dedicated users
	userIds, err := dc.rdb.Keys(dc.ctx, "\\d+").Result()
	if err != nil {
		return
	}
	for _, userId := range userIds {
		currentUserLoadValue, _ := dc.rdb.Get(dc.ctx, userId).Result()
		currentUserLoad, _ := strconv.ParseInt(currentUserLoadValue, 10, 64)
		if currentUserLoad > 1_000_000 { // 1Mb
			userIdInt, _ := strconv.ParseInt(userId, 10, 64)
			dc.dedicatedUsers[userIdInt] = true
		}
	}
	return
}

func connectToHost(user string, password string) (*sql.DB, error) {
	return sql.Open("mysql",
		fmt.Sprintf("%s:%s@tcp(localhost:6033)/dialogues",
			user, password))
}
