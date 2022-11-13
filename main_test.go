package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo"
	"github.com/rinser/hw4/dialogues"
	"github.com/stretchr/testify/assert"
)

var testServer *echo.Echo
var testCoordinator dialogues.Coordinator

func TestMain(m *testing.M) {
	testServer = echo.New()

	testCoordinator = dialogues.NewCoordinator(
		"remote-admin:password@tcp(localhost:6032)/",
		"localhost:7000")

	exitVal := m.Run()
	os.Exit(exitVal)
}

func TestAddUser(t *testing.T) {
	// Setup
	userJSON := `{"login":"user0"}`
	req := httptest.NewRequest(http.MethodPost, "/user",
		strings.NewReader(userJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := testServer.NewContext(req, rec)

	// Assertions
	if assert.NoError(t, testCoordinator.AddUser(c)) {
		assert.Equal(t, http.StatusCreated, rec.Code)
	}
}

func TestAddMessage(t *testing.T) {
	// Setup
	userJSON := `{"login":"user1"}`
	req := httptest.NewRequest(http.MethodPost, "/user",
		strings.NewReader(userJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := testServer.NewContext(req, rec)
	if assert.NoError(t, testCoordinator.AddUser(c)) {
		assert.Equal(t, http.StatusCreated, rec.Code)
	}
	userId1, _ := strconv.ParseInt(rec.Body.String(), 10, 64)

	userJSON = `{"login":"user2"}`
	req = httptest.NewRequest(http.MethodPost, "/user",
		strings.NewReader(userJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	c = testServer.NewContext(req, rec)
	if assert.NoError(t, testCoordinator.AddUser(c)) {
		assert.Equal(t, http.StatusCreated, rec.Code)
	}
	userId2, _ := strconv.ParseInt(rec.Body.String(), 10, 64)

	messageJSON := fmt.Sprintf(`{"from":%d,"to":%d,"text":"some message"}`,
		userId1, userId2)
	req = httptest.NewRequest(http.MethodPost, "/message",
		strings.NewReader(messageJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	c = testServer.NewContext(req, rec)

	// Assertions
	if assert.NoError(t, testCoordinator.AddMessage(c)) {
		assert.Equal(t, http.StatusCreated, rec.Code)
	}
}
