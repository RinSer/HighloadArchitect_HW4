package dialogues

import "time"

type User struct {
	Id    int64  `json:"id"`
	Login string `json:"name"`
}

type Message struct {
	From int64     `json:"from"`
	To   int64     `json:"to"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}
