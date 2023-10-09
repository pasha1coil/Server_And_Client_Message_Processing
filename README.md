# Клиент Серверная архитектура
<details>
  <summary>Содержание</summary>
  <ol>
    <li><a href="#оригинальное-задание">Оригинальное задание</a></li>
    <li><a href="#установка-и-запуск">Установка и запуск</a></li>
  </ol>
</details>

## Оригинальное задание

Задача: реализовать клиент-серверную архитектуру и покрыть её тестами
Сервер:

В цикле генерирует структуры типа msg описанную ниже. Сгенерированные структуры отправляет "свободным" клиентам по очереди. Клиент считается свободным, если не получил сообщений или отчитался о выполнении. Отправлять клиенту сообщения в количестве batchsize. В случае, если msg было отправлено, но в report не был получен его id, отправлять повторно, до трёх раз. в случае третьей неудачи, сохранить в bbolt хранилище неуспешное сообщение. Ни один из msg не должен обрабатываться двумя клиентами одновременно
имеет 2 эндпоинта: /task и /report
/task - принимает через query параметр batchsize и отдаёт клиенту по sse структуры 

type msg struct {

Id string // https://github.com/rs/xid

Period uint64 // 1-1000 random

}
/report - принимает массив id структур описанных выше и помечает их обарботанными. тем или иным образом, на ваше усмотрение. уже обработанные msg повторно отправляться не должны

Клиент:
При запуске случайным образом генерирует batchsize от 2 до 10
подключается к серверу по sse к эндпоинту /task
при получении msg создаёт горутину, которая будет спать Period миллисекунд
если пришло msg, но уже запущено batchsize горутин - обработать ошибку и не создавать новую горутину.
если Period > 800, горутина должна запаниковать и завершиться, но не клиент.
если Period > 900, должен завершиться клиент.

как только все batchsize горутин так или иначе завершатся, отправить массив их id на /report

## Установка и запуск

Клонировать проект.
Через `makefile` можно запустить тесты командой - 'make test'
Если тесты показывают ОК, можно протестировать работу сервера и клиента
Запустить сервер в отдельном терминале, перейдя в каталог сервера коммандой go run server.go
Запустить n - количество клиентов, создав n - количество терминалов и прописав в n - терминалах команду go run client.go, терминалы должны находится в каталоге client