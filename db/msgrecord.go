package db

import (
	"database/sql"
	"fmt"
	"github.com/cohesion-org/deepseek-go"
	"github.com/yincongcyincong/telegram-deepseek-bot/metrics"
	"log"
	"sync"
	"time"
)

const MaxQAPair = 10

type MsgRecordInfo struct {
	AQs        []*AQ
	updateTime int64
}

type AQ struct {
	Question string
	Answer   string
	Token    int
}

type Record struct {
	ID       int
	UserId   int64
	Question string
	Answer   string
	Token    int
}

var MsgRecord = sync.Map{}

func InsertMsgRecord(userId int64, aq *AQ, insertDB bool) {
	var msgRecord *MsgRecordInfo
	msgRecordInter, ok := MsgRecord.Load(userId)
	if !ok {
		msgRecord = &MsgRecordInfo{
			AQs:        []*AQ{aq},
			updateTime: time.Now().Unix(),
		}
	} else {
		msgRecord = msgRecordInter.(*MsgRecordInfo)
		msgRecord.AQs = append(msgRecord.AQs, aq)
		if len(msgRecord.AQs) > MaxQAPair {
			msgRecord.AQs = msgRecord.AQs[1:]
		}
		msgRecord.updateTime = time.Now().Unix()
	}
	MsgRecord.Store(userId, msgRecord)

	if insertDB {
		go insertRecord(&Record{
			UserId:   userId,
			Question: aq.Question,
			Answer:   aq.Answer,
			Token:    aq.Token,
		})
	}
}

func GetMsgRecord(userId int64) *MsgRecordInfo {
	msgRecord, ok := MsgRecord.Load(userId)
	if !ok {
		return nil
	}
	return msgRecord.(*MsgRecordInfo)
}

func DeleteMsgRecord(userId int64) {
	MsgRecord.Delete(userId)
	err := DeleteRecord(userId)
	if err != nil {
		log.Printf("Error deleting record: %v \n", err)
	}
}

func StarCheckUserLen() {
	InsertRecord()
	go func() {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("StarCheckUserLen panic err:%v\n", err)
			}
		}()
		timer := time.NewTicker(time.Minute)
		for range timer.C {
			UpdateDBData()
		}

	}()
}

func UpdateDBData() {
	totalNum := 0
	timeUserPair := make(map[int64][]int64)
	MsgRecord.Range(func(k, v interface{}) bool {
		msgRecord := v.(*MsgRecordInfo)
		if _, ok := timeUserPair[msgRecord.updateTime]; !ok {
			timeUserPair[msgRecord.updateTime] = make([]int64, 0)
		}
		timeUserPair[msgRecord.updateTime] = append(timeUserPair[msgRecord.updateTime], k.(int64))
		UpdateUserInfo(k.(int64), msgRecord.updateTime)
		totalNum++
		return true
	})
}

func UpdateUserInfo(userId int64, updateTime int64) {
	err := UpdateUserUpdateTime(userId, updateTime)
	if err != nil {
		log.Printf("StarCheckUserLen UpdateUserUpdateTime err:%v\n", err)
	}
}

func InsertRecord() {
	users, err := GetUsers()
	if err != nil {
		log.Printf("InsertRecord GetUsers err:%v\n", err)
	}

	for _, user := range users {
		records, err := getRecordsByUserId(user.UserId)
		if err != nil {
			log.Printf("InsertRecord GetUsers err:%v\n", err)
		}
		for _, record := range records {
			InsertMsgRecord(user.UserId, &AQ{
				Question: record.Question,
				Answer:   record.Answer,
			}, false)
			metrics.TotalRecords.Inc()
		}
	}

	metrics.TotalUsers.Add(float64(len(users)))

}

// getRecordsByUserId get latest 10 records by user_id
func getRecordsByUserId(userId int64) ([]Record, error) {
	// construct SQL statements
	query := fmt.Sprintf("SELECT id, user_id, question, answer FROM records WHERE user_id =  ? and is_deleted = 0 limit 10")

	// execute query
	rows, err := DB.Query(query, userId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var record Record
		err := rows.Scan(&record.ID, &record.UserId, &record.Question, &record.Answer)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	return records, nil
}

// insertRecord insert record
func insertRecord(record *Record) {
	query := `INSERT INTO records (user_id, question, answer, token, create_time) VALUES (?, ?, ?, ?, ?)`
	_, err := DB.Exec(query, record.UserId, record.Question, record.Answer, record.Token, time.Now().Unix())
	metrics.TotalRecords.Inc()
	if err != nil {
		log.Printf("insertRecord err:%v\n", err)
	}

	user, err := GetUserByID(record.UserId)
	if err != nil {
		log.Printf("Error get user by userid: %v \n", err)
	}

	if user == nil {
		_, err = InsertUser(record.UserId, deepseek.DeepSeekChat)
		if err != nil {
			log.Printf("Error insert user by userid: %v \n", err)
		}
	}

	err = UpdateUserToken(record.UserId, record.Token)
	if err != nil {
		log.Printf("Error update token by user: %v \n", err)
	}
}

// DeleteRecord delete record
func DeleteRecord(userId int64) error {
	query := `UPDATE records set is_deleted = 1 WHERE user_id = ?`
	_, err := DB.Exec(query, userId)
	return err
}

func GetTokenByUserIdAndTime(userId int64, start, end int64) (int, error) {
	querySQL := `SELECT sum(token) FROM records WHERE user_id = ? and create_time >= ? and create_time <= ?`
	row := DB.QueryRow(querySQL, userId, start, end)

	// scan row get result
	var user User
	err := row.Scan(&user.Token)
	if err != nil {
		if err == sql.ErrNoRows {
			// 如果没有找到数据，返回 nil
			return 0, nil
		}
		return 0, err
	}
	return user.Token, nil
}
