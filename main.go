package main

import (
	"github.com/labstack/echo"
	"github.com/rinser/hw4/dialogues"
)

func main() {
	// init echo server
	e := echo.New()
	// create dialogues' coordinator
	dc, err := dialogues.NewCoordinator(
		"remote-admin:password@tcp(localhost:6032)/",
		"localhost:7000")
	if err != nil {
		e.Logger.Fatal(err)
	} else {
		// add api routes
		e.POST("/user", dc.AddUser)
		e.POST("/message", dc.AddMessage)
		e.GET("/dialogue", dc.GetDialogue)
		// run http server
		e.Logger.Fatal(e.Start(":1234"))
	}
}
