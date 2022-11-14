package main

import (
	"encoding/json"
	"fmt"
	"log"
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
var testCoordinator *dialogues.Coordinator

func TestMain(m *testing.M) {
	testServer = echo.New()
	var err error
	testCoordinator, err = dialogues.NewCoordinator(
		"remote-admin:password@tcp(localhost:6032)/",
		"localhost:7000")
	if err != nil {
		log.Fatal(err)
		os.Exit(-1)
	} else {
		defer testCoordinator.CancelCtx()
		exitVal := m.Run()
		os.Exit(exitVal)
	}
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
	userId1, _ := strconv.ParseInt(strings.Trim(rec.Body.String(), "\n"), 10, 64)

	userJSON = `{"login":"user2"}`
	req = httptest.NewRequest(http.MethodPost, "/user",
		strings.NewReader(userJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	c = testServer.NewContext(req, rec)
	if assert.NoError(t, testCoordinator.AddUser(c)) {
		assert.Equal(t, http.StatusCreated, rec.Code)
	}
	userId2, _ := strconv.ParseInt(strings.Trim(rec.Body.String(), "\n"), 10, 64)

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

func TestGetDialogue(t *testing.T) {
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
	userId1, _ := strconv.ParseInt(strings.Trim(rec.Body.String(), "\n"), 10, 64)

	userJSON = `{"login":"user2"}`
	req = httptest.NewRequest(http.MethodPost, "/user",
		strings.NewReader(userJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	c = testServer.NewContext(req, rec)
	if assert.NoError(t, testCoordinator.AddUser(c)) {
		assert.Equal(t, http.StatusCreated, rec.Code)
	}
	userId2, _ := strconv.ParseInt(strings.Trim(rec.Body.String(), "\n"), 10, 64)

	messageJSON := fmt.Sprintf(`{"from":%d,"to":%d,"text":"some message"}`,
		userId1, userId2)
	req = httptest.NewRequest(http.MethodPost, "/message",
		strings.NewReader(messageJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	c = testServer.NewContext(req, rec)
	if assert.NoError(t, testCoordinator.AddMessage(c)) {
		assert.Equal(t, http.StatusCreated, rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/dialogue?user1=%d&user2=%d", userId1, userId2), nil)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	c = testServer.NewContext(req, rec)
	if assert.NoError(t, testCoordinator.GetDialogue(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		var messages []dialogues.Message
		assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &messages))
		assert.GreaterOrEqual(t, len(messages), 1)
	}
}
