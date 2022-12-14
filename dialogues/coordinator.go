package dialogues

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

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
	CancelCtx      context.CancelFunc
	rdb            *redis.Client
	conn           *proxysql.ProxySQL
	hosts          *sql.DB
	dedicatedHosts *sql.DB
	dedicatedUsers map[int64]bool
}

func NewCoordinator(proxySqlConnection string, redisHost string) (*Coordinator, error) {
	var err error
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		if err != nil {
			cancel()
		}
	}()

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
		ctx:            ctx,
		CancelCtx:      cancel,
		rdb:            rdb,
		conn:           conn,
		dedicatedUsers: make(map[int64]bool),
	}
	err = dc.initHosts()
	if err != nil {
		return nil, err
	}

	return dc, nil
}

func (dc *Coordinator) GetDedicatedUsers() map[int64]bool {
	return dc.dedicatedUsers
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
	_, err = tx.ExecContext(dc.ctx,
		`INSERT INTO users (login) values (?);`, u.Login)
	if err != nil {
		return
	}
	row := tx.QueryRowContext(dc.ctx, `SELECT LAST_INSERT_ID();`)
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
	tag, err := host.ExecContext(dc.ctx, `
	INSERT INTO messages (source, dest, txt, createdAt)
	VALUES (?, ?, ?, CURRENT_TIMESTAMP());`,
		msg.From, msg.To, msg.Text)
	if err != nil {
		return
	}
	numRows, err := tag.RowsAffected()
	if err != nil {
		return
	}
	if numRows != 1 {
		err = fmt.Errorf("could not store message")
		return
	} else {
		go dc.updateHosts(*msg)
		return c.JSON(http.StatusCreated, nil)
	}
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
	msgs := make([]Message, 0)
	userHosts := []*sql.DB{dc.hosts}
	if _, ok := dc.dedicatedUsers[userId1]; ok {
		userHosts = append(userHosts, dc.dedicatedHosts)
	}
	for _, host := range userHosts {
		rows, err := host.QueryContext(dc.ctx,
			`SELECT source, dest, txt, createdAt FROM messages WHERE source = ? and dest = ?;`,
			userId1, userId2)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			msg := Message{}
			err := rows.Scan(&msg.From, &msg.To, &msg.Text, &msg.At)
			if err != nil {
				return nil, err
			}
			msgs = append(msgs, msg)
		}
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
	for port := 3301; port < 3306; port++ {
		user, password := "test1", "test1"
		if port > 3303 {
			user, password = "test2", "test2"
		}
		var db *sql.DB
		db, err = sql.Open("mysql",
			fmt.Sprintf("%s:%s@tcp(localhost:%d)/dialogues",
				user, password, port))
		if err != nil {
			return
		}
		_, err = db.ExecContext(dc.ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id    BIGINT AUTO_INCREMENT PRIMARY KEY,
			login VARCHAR(25)
		);`)
		if err != nil {
			return
		}
		_, err = db.ExecContext(dc.ctx, `
		CREATE TABLE IF NOT EXISTS messages (
			source      BIGINT,
			dest        BIGINT,
			txt         VARCHAR(512),
			createdAt   TIMESTAMP,
			PRIMARY KEY(source, dest, txt, createdAt)
		);`)
		if err != nil {
			return
		}
	}
	// add proxysql hostgroups connections
	dc.hosts, err = connectToHost("test1", "test1")
	if err != nil {
		return
	}
	dc.dedicatedHosts, err = connectToHost("test2", "test2")
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
		fmt.Sprintf("%s:%s@tcp(localhost:6033)/dialogues?parseTime=true",
			user, password))
}
