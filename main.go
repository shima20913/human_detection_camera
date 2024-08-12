package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-resty/resty/v2"
	"github.com/joho/godotenv"
)

type Status struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type Head struct {
	Method  string `json:"method"`
	Service string `json:"service"`
	Time    int    `json:"time"`
}

type BBox struct {
	Xmin float64 `json:"xmin"`
	Ymin float64 `json:"ymin"`
	Xmax float64 `json:"xmax"`
	Ymax float64 `json:"ymax"`
}

type Class struct {
	BBox BBox    `json:"bbox"`
	Prob float64 `json:"prob"`
	Cat  string  `json:"cat"`
	Last bool    `json:"last,omitempty"`
}

type Prediction struct {
	URI     string   `json:"uri"`
	Classes []Class  `json:"classes"`
	Images  []string `json:"images"`
}

type Body struct {
	Predictions []Prediction `json:"predictions"`
}

type Response struct {
	Status Status `json:"status"`
	Head   Head   `json:"head"`
	Body   Body   `json:"body"`
}

const (
	maxImages = 5
)

var (
	imageQueue []string
	queueMutex sync.Mutex
)

func main() {

	godotenv.Load(".env")

	http.HandleFunc("/upload", uploadHandler)
	if err := http.ListenAndServe(":8081", nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	// output log
	log.Printf("Received request: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error reading file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	tempFile, err := os.CreateTemp("/tmp", "upload-*.jpg")
	if err != nil {
		http.Error(w, "Error creating temp file", http.StatusInternalServerError)
		return
	}
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		http.Error(w, "Error saving file", http.StatusInternalServerError)
		return
	}

	imagePath := tempFile.Name()

	response, err := detectObjects("http://100.95.55.91:8080/predict", imagePath)
	if err != nil {
		http.Error(w, "Error detecting objects", http.StatusInternalServerError)
		return
	}

	processPredictions(response, imagePath)

	manageQueue(imagePath)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Successed processing"))
}

func manageQueue(filename string) {
	queueMutex.Lock()
	defer queueMutex.Unlock()

	imageQueue = append(imageQueue, filename)
	if len(imageQueue) > maxImages {
		oldest := imageQueue[0]
		imageQueue = imageQueue[1:]
		os.Remove(oldest)
	}
}

func detectObjects(url, imagePath string) (*Response, error) {
	client := resty.New()

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]interface{}{
			"service": "detection_600",
			"parameters": map[string]interface{}{
				"mllib": map[string]bool{"gpu": true},
				"output": map[string]interface{}{
					"confidence_threshold": 0.3,
					"bbox":                 true,
				},
			},
			"data": []string{filepath.Base(imagePath)},
		}).
		Post(url)
	if err != nil {
		return nil, fmt.Errorf("error sending request to DeepDetect: %w", err)
	}
	log.Printf("DeepDetect response: %s", resp.String())

	var response Response
	err = json.Unmarshal(resp.Body(), &response)
	if err != nil {
		return nil, fmt.Errorf("error decoding JSON response: %w", err)
	}

	return &response, nil

}

func processPredictions(response *Response, imagePath string) {
	for _, prediction := range response.Body.Predictions {
		for _, class := range prediction.Classes {
			log.Printf("Detected %s", class.Cat)
			if class.Cat == "Person" {
				log.Println("Person Detected")
				err := sendToDiscord(imagePath)
				if err != nil {
					log.Printf("Error sending to Discord: %v", err)
				} else {
					fmt.Println("Image sent to Discord successfully.")
				}
				sendNextToImages(imagePath)

				return
			}
		}
	}
	log.Println("Person Not Detected")
}

func sendNextToImages(detectedImagePath string) {
	queueMutex.Lock()
	defer queueMutex.Unlock()
	var index int
	for i, img := range imageQueue {
		if img == detectedImagePath {
			index = i
			break
		}
	}
	var imagesToSend []string
	if index > 0 {
		imagesToSend = append(imagesToSend, imageQueue[index-1])
	}
	imagesToSend = append(imagesToSend, imageQueue[index])
	if index < len(imageQueue)-1 {
		imagesToSend = append(imagesToSend, imageQueue[index+1])
	}
	for _, img := range imagesToSend {
		err := sendToDiscord(img)
		if err != nil {
			log.Printf("Error sending to Discord: %v", err)
		} else {
			fmt.Println("Image sent to Discord successfully.")
		}
	}
	newQueue := make([]string, 0, len(imageQueue))
	for i, img := range imageQueue {
		if i < index-1 || i > index+1 {
			newQueue = append(newQueue, img)
		} else {
			os.Remove(img)
		}
	}
	imageQueue = newQueue
}

func sendToDiscord(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, err := w.CreateFormFile("file", filepath.Base(file.Name()))
	if err != nil {
		return err
	}
	if _, err = io.Copy(fw, file); err != nil {
		return err
	}
	w.Close()

	req, err := http.NewRequest("POST", os.Getenv("DISCORD_WEBHOOK"), &b)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected response status: %s", resp.Status)
	}
	return err
}
