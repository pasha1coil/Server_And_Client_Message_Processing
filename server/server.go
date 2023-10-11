package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/pasha1coil/Server_And_Client_Message_Processing/models"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/boltdb/bolt"
	"github.com/gorilla/mux"
	"github.com/rs/xid"
)

// server структура сервера
type server struct {
	clients          []*client        //подключенные клиенты
	msgQueue         chan *models.Msg //очередь сообщений
	reportedMsgs     map[string]bool
	reportedMsgsMu   sync.Mutex
	db               *bolt.DB //бд болт
	dbBucketName     []byte
	sentMsgs         map[string]int  //мапа ключ - сообщение, значение - количество попыток
	sentdoubleMsgs   map[string]bool //мапа чтобы предотвратить отправку одного и того же сообщения клиентам
	sentdoubleMsgsMu sync.Mutex
	sentMsgCheck     []*models.Msg
	sentMsgCheckMu   sync.Mutex
}

// client структура клиента
type client struct {
	ID         string
	MsgChan    chan *models.Msg // отправляются значения типа *msg
	disconnect chan bool        // канал для отлеживания отключения клиентов
}

var generatedIDs = make(map[string]bool) // проверка на повторяющееся значение генераторе id

// newServer создает новый экземпляр сервера
func newServer() (*server, error) {
	log.Infoln("Создание нового сервера.")
	if err := initConfig(); err != nil {
		log.Fatalf("ошибка инициализации конфига: %s", err.Error())
		return nil, err
	}
	db, err := bolt.Open(viper.GetString("db.Name"), 0600, nil) //открываем болт
	if err != nil {
		log.Error("ошибка открытия базы данных: ", err)
		return nil, err
	}

	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(viper.GetString("db.Bucket"))) //создаем контейнер
		if err != nil {
			log.Errorf("ошибка создания контейнера: %v", err)
			return err
		}
		return nil
	})
	if err != nil {
		log.Errorf("ошибка инициализации базы данных: %v", err)
		return nil, err
	}

	log.Infoln("База данных инициализирована успешно.")

	//заполнение структуры server
	srv := &server{
		sentMsgs:       make(map[string]int),
		msgQueue:       make(chan *models.Msg),
		reportedMsgs:   make(map[string]bool),
		db:             db,
		dbBucketName:   []byte((viper.GetString("db.Bucket"))),
		sentdoubleMsgs: make(map[string]bool),
	}

	log.Infoln("Запуск процедуры обработки сообщений.")
	go srv.processMessages()

	return srv, nil
}

// generateID генерирует уникальный идентификатор, решил сделать рекурсией чтобы id не повторялись
func generateID() string {
	id := xid.New().String()
	log.Infof("Генерация нового ID: %s", id)
	if !generatedIDs[id] {
		generatedIDs[id] = true
		return id
	}
	return generateID()
}

// generateRandomPeriod генерирует случайный период
func generateRandomPeriod() uint64 {
	period := uint64(rand.Intn(1000) + 1)
	log.Infof("Генерация рандомного периода: %d", period)
	return period
}

// функция обработки сообщения
func (srv *server) processMessages() {
	for msg := range srv.msgQueue {
		log.Infof("Обработка сообщения: %s", msg.ID)

		if len(srv.clients) > 0 {
			for countt := 0; countt < 3; countt++ {
				if len(srv.clients) == 0 {
					log.Infof("Нет активных клиентов, спать 3 секунды")
					time.Sleep(3 * time.Second)
					continue
				}
				//лочим блок кода чтобы не было условий гонки и только одна горутина имелла доступ в определенный период времени
				srv.sentdoubleMsgsMu.Lock()
				// Если сообщение уже было отправлено, увеличиваем счетчик отправок
				if _, ok := srv.sentMsgs[msg.ID]; ok {
					srv.sentMsgs[msg.ID]++
				} else {
					srv.sentMsgs[msg.ID] = 1
				}

				if !srv.sentdoubleMsgs[msg.ID] {
					//берем рандомного клиента из списка
					client := srv.clients[rand.Intn(len(srv.clients))]
					log.Infof("Отправка сообщения: %s клиенту: %s", msg.ID, client.ID)

					select {
					//записываем в канал клиента
					case client.MsgChan <- msg:
						srv.sentdoubleMsgs[msg.ID] = true
					case <-client.disconnect:
						srv.clients = removeClient(srv.clients, client.ID)
						srv.sentdoubleMsgsMu.Unlock()
						continue
					default:
						log.Infof("Невозможно отправить сообщение: %s клиенту: %s", msg.ID, client.ID)
					}
				} else if count, ok := srv.sentMsgs[msg.ID]; ok && count == 3 {
					log.Infof("Сообщение %s уже отправлено 3 раза, добавление в БД и пропуск", msg.ID)
					srv.storeMessage(msg)
				} else {
					log.Infof("Сообщение уже было отправленно: %s.", msg.ID)
					countt = 3
				}
				srv.sentdoubleMsgsMu.Unlock()

			}
		} else {
			log.Infof("Нет активных клиентов, спать 3 секунды")
			time.Sleep(3 * time.Second)
		}
	}
}

// Сохранение неуспешно отправленных сообщений в bolt
func (srv *server) storeMessage(m *models.Msg) error {
	err := srv.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(srv.dbBucketName)
		if b == nil {
			return fmt.Errorf("контейнер %q не найден", srv.dbBucketName)
		}

		encoded, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("не удалось сериализовать сообщение: %v", err)
		}

		err = b.Put([]byte(m.ID), encoded)
		if err != nil {
			return fmt.Errorf("не удалось поместить сообщение в контейнер: %v", err)
		}
		return nil
	})

	if err != nil {
		log.Errorf("Ошибка обновления БД: %v", err)
		return err
	}

	return nil
}

// sendMessages обрабатывает запросы на /task и отправляет сообщения клиенту
func (srv *server) sendMessages(w http.ResponseWriter, r *http.Request) {
	log.Infoln("Получение /task request.")

	// по сути этот блок кода можно убрать, так как он в основном должен использоваться для того чтобы отправлять пакет
	// сообщений в соответсвии с тем сколько может принять клиент
	batchSize := 1
	if q := r.URL.Query().Get("batchsize"); q != "" {
		fmt.Sscanf(q, "%d", &batchSize)
	}
	//

	//определяем основные заголовки для sse
	w.Header().Set("Content-Type", "text/event-stream") //текст
	w.Header().Set("Cache-Control", "no-cache")         //без кеша
	w.Header().Set("Connection", "keep-alive")          //соединение отается открытым для последующих HTTP-запросов
	w.Header().Set("X-Accel-Buffering", "no")           //без буфферизации

	//определяем flusher для быстрой передачи данных
	flusher, _ := w.(http.Flusher)

	// создаем канал коиента и определяем структуру клиента
	MsgChan := make(chan *models.Msg)
	client := &client{
		ID:         generateID(),
		MsgChan:    MsgChan,
		disconnect: make(chan bool),
	}

	// обработка закрытия соединения
	if c, ok := w.(http.CloseNotifier); ok {
		closenotify := c.CloseNotify()
		go func() {
			select {
			case <-closenotify:
				client.disconnect <- true
			}
		}()
	}

	srv.clients = append(srv.clients, client)

	log.Infof("Новый клиент %s добавлен.", client.ID)

	//чистим данные клиента после его отключения
	defer func() {
		//лочим блок кода чтобы не было условий гонки и только одна горутина имелла доступ в определенный период времени
		srv.reportedMsgsMu.Lock()
		delete(srv.reportedMsgs, client.ID)
		srv.reportedMsgsMu.Unlock()

		// перед тем как выйти, отправляем сигнал об отключении
		client.disconnect <- true

		//заполняем слайс клиентов
		srv.clients = removeClient(srv.clients, client.ID)
		close(client.MsgChan)
	}()

	//процесс отправки клиенту сообщений по sse
	for msg := range MsgChan {
		srv.sentMsgCheck = append(srv.sentMsgCheck, msg)
		//воркер для проверки репортов
		go srv.CheckReported()
		data, err := json.Marshal(msg)
		if err != nil {
			log.Infof("Не удалось сериализовать сообщение: %v", err)
			continue
		}

		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
}

// reportMessage обрабатывает запросы на /report и помечает сообщения как обработанные
func (srv *server) reportMessage(w http.ResponseWriter, r *http.Request) {
	log.Infoln("Получение /report request.")

	var id string
	err := json.NewDecoder(r.Body).Decode(&id)
	if err != nil {
		http.Error(w, "Ошибка при декодировании JSON", http.StatusBadRequest)
		return
	}

	//заполняем инфу о то что пришел отчет о обработанном сообщении
	//лочим блок кода чтобы не было условий гонки и только одна горутина имелла доступ в определенный период времени
	srv.reportedMsgsMu.Lock()
	log.Infof("Отчетное соообщение %s.", id)
	srv.reportedMsgs[id] = true
	delete(srv.sentMsgs, id)

	srv.reportedMsgsMu.Unlock()

	//Успех
	w.WriteHeader(http.StatusOK)
}

// removeClient удаляет клиента из списка клиентов
func removeClient(clients []*client, clientID string) []*client {
	log.Infof("Удаление клиента: %s", clientID)
	result := []*client{}
	for _, client := range clients {
		if client.ID != clientID {
			result = append(result, client)
		}
	}
	return result
}

// воркер для чека репортов, если репорт не получен то данные о msg заносятся в бд
func (srv *server) CheckReported() {
	if len(srv.sentMsgCheck) != 0 {
		srv.sentMsgCheckMu.Lock()
		defer srv.sentMsgCheckMu.Unlock()

		for count := 0; count < 3; count++ {
			if len(srv.sentMsgCheck) > 0 {
				id := srv.sentMsgCheck[0].ID
				log.Infof("Проверка отчета о msg: %s", id)
				if srv.reportedMsgs[id] {
					srv.sentMsgCheck = srv.sentMsgCheck[1:]
				} else {
					if count == 2 {
						log.Infof("Добавление msg: %s в бд", id)
						err := srv.storeMessage(srv.sentMsgCheck[0])
						if err != nil {
							log.Errorf("ошибка добавления msg: %s в бд", id)
						}
						srv.sentMsgCheck = srv.sentMsgCheck[1:]
						break
					}
				}
				time.Sleep(3 * time.Second)
			} else {
				count = 0
				time.Sleep(10 * time.Second)
			}
		}
	}
}

func main() {
	log.SetFormatter(new(log.JSONFormatter))
	log.Infof("Инициализация сервера.")
	srv, err := newServer()
	if err != nil {
		log.Errorf("Ошибка инициализации сервера: %v", err)
	}

	//воркер который генерит стукртуры Msg
	go func() {
		for {
			period := generateRandomPeriod()
			msg := &models.Msg{
				ID:     generateID(),
				Period: period,
			}
			log.Infof("Добавление сообщения %s в очередь", msg.ID)
			srv.msgQueue <- msg
			time.Sleep(time.Second) // ждем перед генерацией следующего сообщения
		}
	}()

	// GracefulShuttdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		log.Infoln("Завершение работы сервера, закрытие базы данных.")
		srv.db.Close()
		os.Exit(0)
	}()

	// init routes
	router := mux.NewRouter()
	router.HandleFunc("/task", srv.sendMessages).Methods("GET")
	router.HandleFunc("/report", srv.reportMessage).Methods("POST")

	log.Infoln("Сервер запущен на http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", router))
}

func initConfig() error {
	viper.AddConfigPath("configs")
	viper.SetConfigName("config")
	return viper.ReadInConfig()
}
