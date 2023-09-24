package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"os"
	"time"

	"net/http"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/postgres"
	"github.com/nats-io/stan.go"
	"github.com/patrickmn/go-cache"
)

type MessageData struct {
	ID        uint `gorm:"76V8V74ds8YrSL1yfREJUf"`
	Timestamp time.Time
	Data      []byte
}

type JSONData struct {
	OrderUID          string   `json:"order_uid"`
	TrackNumber       string   `json:"track_number"`
	Entry             string   `json:"entry"`
	Delivery          delivery `json:"delivery"`
	Payment           payment  `json:"payment"`
	Items             []items  `json:"items"`
	Locale            string   `json:"locale"`
	InternalSignature string   `json:"internal_signature"`
	CustomerID        string   `json:"customer_id"`
	DeliveryService   string   `json:"delivery_service"`
	ShardKey          string   `json:"shardkey"`
	SMID              int      `json:"sm_id"`
	DateCreated       string   `json:"date_created"`
	OOFShard          string   `json:"oof_shard"`
}

type delivery struct {
	Name    string `json:"name"`
	Phone   string `json:"phone"`
	Zip     string `json:"zip"`
	City    string `json:"city"`
	Address string `json:"address"`
	Region  string `json:"region"`
	Email   string `json:"email"`
}

type payment struct {
	Transaction  string `json:"transaction"`
	RequestID    string `json:"request_id"`
	Currency     string `json:"currency"`
	Provider     string `json:"provider"`
	Amount       int    `json:"amount"`
	PaymentDT    int64  `json:"payment_dt"`
	Bank         string `json:"bank"`
	DeliveryCost int    `json:"delivery_cost"`
	GoodsTotal   int    `json:"goods_total"`
	CustomFee    int    `json:"custom_fee"`
}

type items struct {
	ChrtID      int    `json:"chrt_id"`
	TrackNumber string `json:"track_number"`
	Price       int    `json:"price"`
	RID         string `json:"rid"`
	Name        string `json:"name"`
	Sale        int    `json:"sale"`
	Size        string `json:"size"`
	TotalPrice  int    `json:"total_price"`
	NMID        int    `json:"nm_id"`
	Brand       string `json:"brand"`
	Status      int    `json:"status"`
}

func readModelDataFromFile(filename string) (*JSONData, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var jsonData JSONData
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&jsonData)
	if err != nil {
		return nil, err
	}

	return &jsonData, nil
}

func autoMigrateTables(db *gorm.DB) {
	if !db.HasTable(&JSONData{}) {
		db.AutoMigrate(&JSONData{})
		fmt.Println("Таблица JSONData создана")
	}
	if !db.HasTable(&delivery{}) {
		db.AutoMigrate(&delivery{})
		fmt.Println("Таблица delivery создана")
	}
	if !db.HasTable(&payment{}) {
		db.AutoMigrate(&payment{})
		fmt.Println("Таблица payment создана")
	}
	if !db.HasTable(&items{}) {
		db.AutoMigrate(&items{})
		fmt.Println("Таблица items создана")
	}
}

func getFromCacheAndDB(c *cache.Cache, db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "Отсутствует параметр id", http.StatusBadRequest)
			return
		}

		if data, found := c.Get(id); found {
			fmt.Println("Data from JSON:", data)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			tmpl, err := template.ParseFiles("template.html")
			if err != nil {
				http.Error(w, "Ошибка загрузки шаблона", http.StatusInternalServerError)
				return
			}
			err = tmpl.Execute(w, data)
			if err != nil {
				http.Error(w, "Ошибка отображения данных", http.StatusInternalServerError)
				return
			}
		} else {
			var dbData JSONData
			if err := db.Preload("Delivery").Preload("Payment").Preload("Items").First(&dbData, "order_uid = ?", id).Error; err != nil {
				http.Error(w, "Данные не найдены в кэше и в базе данных", http.StatusNotFound)
				return
			}

			c.Set(dbData.OrderUID, dbData, cache.DefaultExpiration)

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			tmpl, err := template.ParseFiles("template.html")
			if err != nil {
				http.Error(w, "Ошибка загрузки шаблона", http.StatusInternalServerError)
				return
			}
			err = tmpl.Execute(w, dbData)
			if err != nil {
				http.Error(w, "Ошибка отображения данных", http.StatusInternalServerError)
				return
			}
		}
	}
}

func getAllDataFromDB(db *gorm.DB) ([]JSONData, error) {
	var allData []JSONData
	if err := db.Find(&allData).Error; err != nil {
		return nil, err
	}
	return allData, nil
}

func main() {
	db, err := gorm.Open("postgres", "host=localhost port=5432 user=postgres dbname=wb_baza sslmode=disable password=0000")
	if err != nil {
		log.Fatal("Не удалось подключиться к базе данных:", err)
	}
	defer db.Close()

	autoMigrateTables(db)

	nc, err := stan.Connect("test-cluster", "my-subscriber-client")
	if err != nil {
		log.Fatal("Не удалось подключиться к NATS Streaming:", err)
	}
	defer nc.Close()

	modelData, err := readModelDataFromFile("model.json")
	if err != nil {
		log.Fatal("Не удалось прочитать данные из файла:", err)
	}

	err = db.Create(modelData).Error
	if err != nil {
		log.Println("Не удалось вставить данные JSON в базу данных:", err)
		return
	}

	deliveryData := modelData.Delivery
	err = db.Create(&deliveryData).Error
	if err != nil {
		log.Println("Не удалось вставить данные delivery в базу данных:", err)
		return
	}

	paymentData := modelData.Payment
	err = db.Create(&paymentData).Error
	if err != nil {
		log.Println("Не удалось вставить данные payment в базу данных:", err)
		return
	}

	for _, item := range modelData.Items {
		err = db.Create(&item).Error
		if err != nil {
			log.Println("Не удалось вставить данные item в базу данных:", err)
			return
		}
	}

	fmt.Println("Данные из файла model.json вставлены в базу данных")

	_, err = nc.Subscribe("my-channel", func(msg *stan.Msg) {
		var jsonData JSONData
		err := json.Unmarshal(msg.Data, &jsonData)
		if err != nil {
			log.Println("Ошибка при разборе JSON:", err)
			return
		}

		err = db.Create(&jsonData).Error
		if err != nil {
			log.Println("Не удалось вставить данные JSON в базу данных:", err)
			return
		}

		deliveryData := jsonData.Delivery
		err = db.Create(&deliveryData).Error
		if err != nil {
			log.Println("Не удалось вставить данные delivery в базу данных:", err)
			return
		}

		paymentData := jsonData.Payment
		err = db.Create(&paymentData).Error
		if err != nil {
			log.Println("Не удалось вставить данные payment в базу данных:", err)
			return
		}

		for _, item := range jsonData.Items {
			err = db.Create(&item).Error
			if err != nil {
				log.Println("Не удалось вставить данные item в базу данных:", err)
				return
			}
		}

		fmt.Printf("Принятое сообщение: %s\n", string(msg.Data))
	})
	if err != nil {
		log.Fatal("Не удалось подписаться:", err)
	}
	c := cache.New(5*time.Minute, 10*time.Minute)
	c.Set(modelData.OrderUID, modelData, cache.DefaultExpiration)
	http.HandleFunc("/get", getFromCacheAndDB(c, db))
	http.ListenAndServe(":8080", nil)
}
