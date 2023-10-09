package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/r3labs/sse"
	log "github.com/sirupsen/logrus"
)

type Message struct {
	ID     string
	Period uint64
}

func main() {
	log.SetFormatter(new(log.JSONFormatter))
	log.Infoln("Запуск приложения")
	batchSize := 2 + rand.Intn(9)
	log.Infof("Batch size: %d", batchSize)

	client := initClient(batchSize)
	log.Infoln("Инициализация SSE клиента")

	var goroutinesCount int32
	var wg sync.WaitGroup
	wg.Add(batchSize)

	var msgIDMutex = &sync.Mutex{}
	exitFlag := make(chan bool)
	msgsSeen := make(map[string]bool)      //проверка на повтор
	msgIDs := make([]string, 0, batchSize) //отработанные id msg

	ch := make(chan *sse.Event)
	subscribeChannel(client, "message", ch)
	log.Infoln("Подписался на SSE-канал 'message'")

	go readMessage(ch, &wg, &goroutinesCount, exitFlag, msgIDMutex, msgsSeen, msgIDs, batchSize)

	wg.Wait()
	log.Infoln("Все горутины завершились")
	log.Infoln("Завершены все задачи")
}

// иницилизируем клиента
func initClient(batchSize int) *sse.Client {
	client := sse.NewClient("http://localhost:8080/task?batchsize=" + strconv.Itoa(batchSize))
	return client
}

// подписывваемся на канал
func subscribeChannel(client *sse.Client, name string, ch chan *sse.Event) {
	client.SubscribeChan(name, ch)
}

// обрабатываем сообщение
func processMessage(m *Message, exitFlag chan bool, wg *sync.WaitGroup, msgIDMutex *sync.Mutex, msgIDs []string, goroutinesCount *int32, batchSize int) {
	defer func() {
		if r := recover(); r != nil {
			log.Infof("Восстановлено в горутине для msg %s: %v", m.ID, r)
		}
	}()

	if atomic.AddInt32(goroutinesCount, 1) > int32(batchSize) {
		log.Infof("Batchsize лимит для msg: %s. Игнорирование.", m.ID)
		atomic.AddInt32(goroutinesCount, -1)
		return
	}

	defer atomic.AddInt32(goroutinesCount, -1)
	defer wg.Done()

	msgIDMutex.Lock()
	msgIDs = append(msgIDs, m.ID)
	msgIDMutex.Unlock()

	if m.Period > 900 {
		log.Infof("Период > 900 для msg: %s. Добавление значения в exitFlag -> true", m.ID)
		exitFlag <- true
		os.Exit(1) // резкое завершение
	} else if m.Period > 800 {
		panic("Горутина паникует так как Период > 800")
	}

	// спим как указано в тз
	time.Sleep(time.Duration(m.Period) * time.Millisecond)

	//отправляем репорт на сервер
	sendReport(m.ID)
}

// функция чтения и обработки приходящих сообщений
func readMessage(ch chan *sse.Event, wg *sync.WaitGroup, goroutinesCount *int32, exitFlag chan bool, msgIDMutex *sync.Mutex, msgsSeen map[string]bool, msgIDs []string, batchSize int) {
	for {
		select {
		case msg := <-ch:
			m := new(Message)
			if err := json.Unmarshal(msg.Data, m); err != nil {
				log.Errorln("Error unmarshalling data:", err)
				return
			}
			log.Infof("Получено сообщение с ID: %s и Периодом: %d", m.ID, m.Period)

			// Проверка наличия сообщения в мапе
			msgIDMutex.Lock()
			if msgsSeen[m.ID] {
				log.Infof("Уже обработано сообщение с ID: %s. Пропускаем.", m.ID)
				msgIDMutex.Unlock()
				continue
			}
			msgsSeen[m.ID] = true
			msgIDMutex.Unlock()

			go processMessage(m, exitFlag, wg, msgIDMutex, msgIDs, goroutinesCount, batchSize)
		case <-exitFlag:
			return
		}
	}
}

// отправка report о обработанных сообщениях на сервер
func sendReport(id string) {
	// Передаем отчет после обработки сообщения
	report, err := json.Marshal(id) // Указываем только ID обработанного сообщения
	if err != nil {
		log.Infoln("Error marshalling ID:", err)
		return
	}

	res, err := http.Post("http://localhost:8080/report", "application/json", bytes.NewBuffer(report))
	if err != nil {
		log.Errorln("Ошибка отправки POST запроса на /report:", err)
		return
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
}
