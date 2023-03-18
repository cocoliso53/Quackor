package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	openai "github.com/sashabaranov/go-openai"
)

// Make global variable telegram bot, check how to better this

// Local database might not be needed

type WebhookResponse struct {
	TranscriptID string `json:"transcript_id"`
	Status       string `json:"status"`
}

type TranscriptionResponse struct {
	Text string `json:"text"`
}

type FileDetails struct {
	ChatID     string `json:"chatID"`
	FileURL    string `json:"fileURL"`
	AssemblyID string `json:"assemblyID"`
}

type File struct {
	FileID  string      `json:"fileID"`
	Details FileDetails `json:"details"`
}

var db = map[string]FileDetails{}

// AssemblyAI API
func UploadAudio(audioPATH string, apiKey string) {

	// Constants
	const UPLOAD_URL = "https://api.assemblyai.com/v2/upload"

	data, err := os.ReadFile(audioPATH)

	if err != nil {
		fmt.Println("Error 1")
	}

	client := &http.Client{}
	req, _ := http.NewRequest("POST", UPLOAD_URL, bytes.NewBuffer(data))
	req.Header.Set("authorization", apiKey)
	resp, err := client.Do(req)

	if err != nil {

		fmt.Println("Error 2")
	}

	defer resp.Body.Close()

	// Decode json and store it in a map
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	fmt.Println(result["upload_url"])
}

func TranscribeAudio(audioURL string, chatID int64, apiKey string) {

	const TRANSCRIPT_URL = "https://api.assemblyai.com/v2/transcript"
	var WEBHOOK_URL = fmt.Sprintf(
		"http://d63a-2806-2f0-a0a1-fe7e-cb83-26d6-ca8a-4c37.ngrok.io/transcription/%d",
		chatID)

	client := &http.Client{}
	payload := fmt.Sprintf(`{"audio_url": "%s", "language_code": "es", "webhook_url": "%s"}`,
		audioURL, WEBHOOK_URL)
	data := strings.NewReader(payload)
	req, err := http.NewRequest("POST", TRANSCRIPT_URL, data)
	if err != nil {
		fmt.Println("Error 3")
	}

	req.Header.Set("authorization", apiKey)
	req.Header.Set("content-type", "application/x-www-from-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error 4")
	}

	defer resp.Body.Close()
	bodyText, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Error 5")
	}

	fmt.Println(audioURL)

	fmt.Printf("%s\n", bodyText)
}

func GetTranscription(transcriptionID string, apiKey string) (string, error) {

	var TRANSCRIPT_URL = fmt.Sprintf("https://api.assemblyai.com/v2/transcript/%s", transcriptionID)

	client := &http.Client{}
	req, err := http.NewRequest("GET", TRANSCRIPT_URL, nil)
	if err != nil {
		fmt.Println("Error 6")
		return "", err
	}

	req.Header.Set("authorization", apiKey)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error 7")
		return "", err
	}

	defer resp.Body.Close()

	transcript := new(TranscriptionResponse)
	if err := json.NewDecoder(resp.Body).Decode(transcript); err != nil {
		return "", err
	}

	return transcript.Text, nil
}

func showFiles(c *gin.Context) {
	c.IndentedJSON(http.StatusOK, db)
}

func addFile(c *gin.Context) {
	var newFile File

	if err := c.BindJSON(&newFile); err != nil {
		return
	}

	db[newFile.FileID] = newFile.Details
	c.IndentedJSON(http.StatusCreated, newFile)
}

func chatGPT(text string, apiKEY string) (string, error) {
	c := openai.NewClient(apiKEY)
	resp, err := c.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT3Dot5Turbo,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: text,
				},
			},
		},
	)

	if err != nil {
		fmt.Printf("Error %v\n", err)
		return "", err
	}

	return resp.Choices[0].Message.Content, nil
}

// func serverAPI() {
// 	r := gin.Default()

// 	r.GET("/file", showFiles)
// 	r.POST("/file", addFile)
// 	r.POST("/transcription/:chatID", printTranscript)

// 	r.Run()
// }

func main() {

	err := godotenv.Load("config.env")
	if err != nil {
		log.Fatal("Error cargando la configuración")
	}

	telegramAPI := os.Getenv("TELEGRAM_API")
	assemblyAPI := os.Getenv("ASSEMBLYAI_API")
	openaiAPI := os.Getenv("OPENAI_API")

	bot, err := tgbotapi.NewBotAPI(telegramAPI)

	if err != nil {
		log.Panic(err)
	}

	go func() {

		updateConfig := tgbotapi.NewUpdate(0)
		updateConfig.Timeout = 30

		updates := bot.GetUpdatesChan(updateConfig)

		for update := range updates {
			if update.Message == nil {
				continue
			}

			if update.Message.Voice != nil {
				fileID := update.Message.Voice.FileID
				fileURL, _ := bot.GetFileDirectURL(fileID)
				TranscribeAudio(fileURL, update.Message.Chat.ID, assemblyAPI)
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Procesando...")
				_, err := bot.Send(msg)

				if err != nil {
					log.Printf("Error sending message: %v", err)
				}
			}
		}
	}()

	go func() {
		r := gin.Default()
		r.GET("/file", showFiles)
		r.POST("/file", addFile)
		r.POST("/transcription/:chatID", func(c *gin.Context) {
			id := c.Param("chatID")
			id64, err := strconv.ParseInt(id, 10, 64)
			if err != nil {
				c.IndentedJSON(http.StatusBadRequest, `{"Error":"parseo malo"}`)
			}

			var webhookResponse WebhookResponse

			if err := c.BindJSON(&webhookResponse); err != nil {
				return
			}

			text, err := GetTranscription(webhookResponse.TranscriptID, assemblyAPI)

			if err != nil {
				log.Printf("Error sending message: %v", err)
				c.IndentedJSON(http.StatusBadRequest, `{"Error":"No texto"}`)
			}

			content, err := chatGPT(text, openaiAPI)
			if err != nil {
				log.Printf("Error sending message: %v", err)
				c.IndentedJSON(http.StatusBadRequest, `{"Error":"chatGPT"}`)
			}

			msg := tgbotapi.NewMessage(id64, content)
			_, err = bot.Send(msg)

			if err != nil {
				log.Printf("Error sending message: %v", err)
				c.IndentedJSON(http.StatusBadRequest, `{"Error":"mensaje malo"}`)
			}

			c.IndentedJSON(http.StatusOK, `{"Error"; "nada, todo bien"}`)
			return
		})

		r.Run()
	}()

	select {}
}

// func main() {
// 	r := gin.Default()

// 	s := VoiceID()

// 	r.GET("/", func(c *gin.Context) {
// 		c.JSON(http.StatusOK, gin.H{"data": "lol"})
// 	})

// 	r.Run()
// }
