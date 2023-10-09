package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testingpena/models"
	"time"

	"github.com/boltdb/bolt"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func Test_newServer(t *testing.T) {
	log.Println("Test_newServer")
	if err := initConfig(); err != nil {
		log.Fatalf("ошибка инициализации конфига: %s", err.Error())
	}
	// Задаем временное имя для файла базы данных
	dbFile := viper.GetString("db.Name")
	// Удаляем базу данных после завершения теста
	defer os.Remove(dbFile)

	srv, err := newServer()
	if err != nil {
		t.Fatalf("Ошибка инициализации сервера: %v", err)
	}

	// bolt?
	if srv.db == nil {
		t.Error("Ошибка открытия базы данных")
	}

	// msg.Queue?
	if srv.msgQueue == nil {
		t.Error("Ошибка: канал сообщений не инициализирован")
	}

	// sentMsgs?
	if srv.sentMsgs == nil {
		t.Error("Ошибка: map sentMsgs не создана")
	}

	// reportedMsgs?
	if srv.reportedMsgs == nil {
		t.Error("Ошибка: map reportedMsgs не создана")
	}

	// bolt close?
	err = srv.db.Close()
	if err != nil {
		t.Fatalf("Ошибка при закрытии базы данных: %v", err)
	}
}

func Test_reportMessage(t *testing.T) {
	log.Println("Test_reportMessage")
	if err := initConfig(); err != nil {
		log.Fatalf("ошибка инициализации конфига: %s", err.Error())
	}
	//init server
	srv, err := newServer()
	srv.db.Close()
	os.Remove(viper.GetString("db.Name"))
	//чтобы не было гонки беру require
	require.NoError(t, err)

	t.Run("Успешный", func(t *testing.T) {
		msg := &models.Msg{
			ID:     "dfsdfsvsdvs",
			Period: 100,
		}
		srv.msgQueue <- msg
		// в успешном отправляем сообщение которое можно декодировать
		req := httptest.NewRequest("POST", "/report", strings.NewReader(`"successful"`))
		r := httptest.NewRecorder()
		srv.reportMessage(r, req)

		require.Equal(t, http.StatusOK, r.Code)

		srv.reportedMsgsMu.Lock()
		_, data := srv.reportedMsgs["successful"]
		srv.reportedMsgsMu.Unlock()
		require.True(t, data)
	})

	t.Run("Ошибочный", func(t *testing.T) {
		//в ошибочном отправляем значение которое нельзя декодировать
		req := httptest.NewRequest("POST", "/report", strings.NewReader(`bad`))
		r := httptest.NewRecorder()
		srv.reportMessage(r, req)

		require.Equal(t, http.StatusBadRequest, r.Code)
	})
}

func Test_removeClient(t *testing.T) {
	log.Println("Test_removeClient")
	clients := []*client{
		{
			ID:         "first",
			MsgChan:    make(chan *models.Msg),
			disconnect: make(chan bool),
		},
		{
			ID:         "second",
			MsgChan:    make(chan *models.Msg),
			disconnect: make(chan bool),
		},
		{
			ID:         "third",
			MsgChan:    make(chan *models.Msg),
			disconnect: make(chan bool),
		},
	}

	clientIDToRemove := "first"
	updatedClients := removeClient(clients, clientIDToRemove)

	for _, client := range updatedClients {
		if client.ID == clientIDToRemove {
			t.Errorf("RemoveClient не удалось удалить клиента с ID %s", clientIDToRemove)
		}
	}
}

func Test_generateID(t *testing.T) {
	log.Println("Test_generateID")
	id1 := generateID()
	id2 := generateID()

	if id1 == id2 {
		t.Errorf("Ошибка: ID не уникальны. ID 1: %s, ID 2: %s", id1, id2)
	}

	if _, exists := generatedIDs[id1]; !exists {
		t.Errorf("Ошибка: ID не был сохранен в generatedIDs. ID: %s", id1)
	}
}

func Test_generateRandomPeriod(t *testing.T) {
	log.Println("Test_generateRandomPeriod")
	for i := 0; i < 100; i++ {
		result := generateRandomPeriod()
		if result < 1 || result > 1000 {
			t.Errorf("Получено неправильное значение: %v, ожидается между 1 и 1000", result)
		}
	}
}

func Test_storeMessage(t *testing.T) {
	log.Println("Test_storeMessage")
	//определяеи тестовоую бд и контейнер
	testDb := viper.GetString("test.Name")
	testBucketName := viper.GetString("test.Bucket")
	// определям msg
	msg := &models.Msg{
		ID:     generateID(),
		Period: generateRandomPeriod(),
	}

	//инициализируем бд
	db, _ := bolt.Open(testDb, 0600, nil)
	defer db.Close()
	defer os.Remove(testDb) //удалям после отработки

	err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(testBucketName))
		return err
	})

	if err != nil {
		t.Fatalf("Не удалось создать контейнер в тестовой бд: %v", err)
	}
	//заполняем струкру сервера
	srv := &server{
		db:           db,
		dbBucketName: []byte(testBucketName),
	}
	defer srv.db.Close()

	// сохраняем msg в тестовой бд
	err = srv.storeMessage(msg)
	if err != nil {
		t.Fatalf("Ошибка при сохранении сообщения: %v", err)
	}

	// Проверяем, что сообщение было сохранено корректно
	err = db.View(func(tx *bolt.Tx) error {
		cont := tx.Bucket([]byte(testBucketName))
		if cont == nil {
			return fmt.Errorf("Контейнер %q не найден", testBucketName)
		}

		mes := cont.Get([]byte(msg.ID))
		if mes == nil {
			return fmt.Errorf("Сообщение %s не было сохранено", msg.ID)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("Ошибка при чтении из базы данных: %v", err)
	}
}

func Test_processMessages(t *testing.T) {
	log.Println("Test_processMessages")
	if err := initConfig(); err != nil {
		log.Fatalf("ошибка инициализации конфига: %s", err.Error())
	}
	srv, err := newServer()
	require.NoError(t, err, "Ошибка инициализации сервера")

	defer os.Remove(viper.GetString("db.Name"))
	defer srv.db.Close()

	// Создаем список тестовых сообщений msg
	msgs := []*models.Msg{
		{
			ID:     generateID(),
			Period: uint64(time.Second),
		},
	}

	// Создаем клиента для получения сообщений
	client := &client{
		ID:         generateID(),
		MsgChan:    make(chan *models.Msg),
		disconnect: make(chan bool),
	}

	// Клиент подключается к серверу
	srv.clients = append(srv.clients, client) // записываем в список склиентов - клиента

	done := make(chan bool)

	// Запуск горутины на получение сообщений
	go func() {
		for msg := range client.MsgChan {
			for _, m := range msgs {
				if m.ID == msg.ID {
					done <- true
				}
			}
		}
		close(done)
	}()

	for _, msg := range msgs {
		// Помещение сообщения в очередь
		srv.msgQueue <- msg
		ok := <-done
		require.True(t, ok, "Ошибка при отправке сообщения")
	}

	require.Equal(t, len(srv.sentMsgs), len(msgs), "Не все сообщения были отправлены")

	require.Equal(t, len(client.MsgChan), 0, "Канал сообщений не пуст")
}
