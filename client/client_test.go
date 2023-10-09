package main

import (
	"log"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_initClient(t *testing.T) {
	log.Println("Test_initClient")
	batchSize := 5
	client := initClient(batchSize)

	if client == nil {
		t.Errorf("initClient() вернул nil")
	}

	expectedUrl := "http://localhost:8080/task?batchsize=" + strconv.Itoa(batchSize)
	if client.URL != expectedUrl {
		t.Errorf("unexpected client url, got: %v, want: %v", client.URL, expectedUrl)
	}
	assert.Equal(t, client.URL, expectedUrl)
}
