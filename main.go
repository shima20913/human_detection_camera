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
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/joho/godotenv"
)

type WeatherResponse struct {
	Weather []struct {
		Main string `json:"main"`
	} `json:"weather"`
}

type Status struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type Head struct {
	Method  string  `json:"method"`
	Service string  `json:"service"`
	Time    float64 `json:"time"`
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

type DetectionData struct {
	Image string 
	Time time.Time
}

const (
	maxImages = 5
)

var (
	imageQueue []DetectionData
	queueMutex sync.Mutex
)



func main() {

	godotenv.Load(".env")

	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/detection", detectionHandler)

	//画像取得用のエンドポイントを作成
	http.Handle("/image/", http.StripPrefix("/image", http.FileServer(http.Dir("imagesfile"))))
	
	corsHandler := corsMiddleware(http.DefaultServeMux)

	if err := http.ListenAndServe(":8081", corsHandler); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func getWeatherInfo() (string, error) {
	client := resty.New()

	// APIキーと都市名を取得
	apiKey := os.Getenv("OPENWEATHER_API_KEY")
	city := "Hiroshima" // 都市名を指定

	url := fmt.Sprintf("https://api.openweathermap.org/data/2.5/weather?q=%s&appid=%s", city, apiKey)

	// APIリクエストを送信
	resp, err := client.R().Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to request weather info: %w", err)
	}

	var weatherResponse WeatherResponse
	err = json.Unmarshal(resp.Body(), &weatherResponse)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal weather response: %w", err)
	}

	if len(weatherResponse.Weather) > 0 {
		return weatherResponse.Weather[0].Main, nil
	}

	return "Unknown", nil
}

func detectionHandler(w http.ResponseWriter, r *http.Request) {
	queueMutex.Lock()
	defer queueMutex.Unlock()

	latestDetections := imageQueue
	if len(latestDetections) > 10 {
		latestDetections = latestDetections[len(latestDetections)-10:]
		// latestDetections = latestDetections[:10]
	}

	// 天気情報を取得
	weather, err := getWeatherInfo()
	if err != nil {
		weather = "Unknown"
		log.Printf("failed to get weather info: %v", err)
	}

	detections := []map[string]string{}
	for _, imageData := range latestDetections {
		detections = append(detections, map[string]string{
			"imageUrl": imageData.Image,
			"time":     imageData.Time.Format("2006-01-02 15:04:05"),
			"weather":  weather, // 取得した天気情報を追加
		})
	}

	jsonResponse, err := json.Marshal(detections)
	if err != nil {
		http.Error(w, "Failed to generate JSON", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonResponse)
}


func uploadHandler(w http.ResponseWriter, r *http.Request) {
	// output log
	log.Printf("Received request: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error reading file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	tempFilePath := filepath.Join("imagesfile", "upload-"+fileHeader.Filename)
	tempFile, err := os.Create(tempFilePath)
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

	imagePath := filepath.Base(tempFilePath)

	response, err := detectObjects("http://100.95.55.91:8082/predict", imagePath)
	if err != nil {
		http.Error(w, "Error detecting objects", http.StatusInternalServerError)
		log.Printf("Motion detection failed: %v", err)
		return

	}

	if processPredictions(response, imagePath) {
		manageQueue(imagePath)
	}else{
		os.Remove("./imagesfile/" + imagePath)
	}

	

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Successed processing"))
}

func manageQueue(filename string) {
	queueMutex.Lock()
	defer queueMutex.Unlock()

	

	imageQueue = append(imageQueue, DetectionData{Image: filename, Time: time.Now()})
	if len(imageQueue) > maxImages {
		oldest := imageQueue[0]
		imageQueue = imageQueue[1:]
		os.Remove("./imagesfile/" + oldest.Image)
	}
}

func detectObjects(url, imagePath string) (*Response, error) {
	// if _, err := os.Stat(imagePath); os.IsNotExist(err) {
	// 	return nil, fmt.Errorf("image file does not exist: %w", err)
	// }

	// client := resty.New()

	// file, err := os.Open(imagePath)
	// if err != nil {
	// 	return nil, fmt.Errorf("could not open image file: %w", err)
	// }
	// defer file.Close()

	// var b bytes.Buffer
	// w := multipart.NewWriter(&b)
	// part, err := w.CreateFormFile("file", filepath.Base(imagePath))
	// if err != nil {
	// 	return nil, fmt.Errorf("could not create form file: %w", err)
	// }
	// _, err = io.Copy(part, file)
	// if err != nil {
	// 	return nil, fmt.Errorf("could not copy file to form: %w", err)
	// }
	// w.Close()

	client := resty.New()
	payload := map[string]interface{}{
		"service": "detection_600",
		"parameters": map[string]interface{}{
			"input":  map[string]interface{}{},
			"output": map[string]interface{}{"confidence_threshold": 0.3, "bbox": true},
			"mllib":  map[string]interface{}{"gpu": false},
		},
		"data": []string{"/data/" + imagePath},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("error creating json payload: %w", err)
	}

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(jsonPayload).
		Post(url)
	if err != nil {
		return nil, fmt.Errorf("error sending request to DeepDetect: %w", err)
	}

	var response Response
	err = json.Unmarshal(resp.Body(), &response)
	if err != nil {
		return nil, fmt.Errorf("error decoding JSON response: %w", err)
	}

	return &response, nil

}

func processPredictions(response *Response, imagePath string) bool {
	for _, prediction := range response.Body.Predictions {
		for _, class := range prediction.Classes {
			log.Printf("Detected %s", class.Cat)
			if class.Cat == "Person" || class.Cat == "Face" {
				log.Println("Person Detected")
				err := sendToDiscord(imagePath)
				if err != nil {
					log.Printf("error sending to Discord: %v", err)
				} else {
					fmt.Println("image sent to Discord successfully.")
				}
				// sendNextToImages(imagePath)

				// if err := os.Remove("./imagesfile/" + imagePath); err != nil {
				// 	log.Printf("error deleting image: %v", err)
				// } else {
				// 	log.Println("image deleted successfully:", imagePath)
				// }

				return true
			}
		}
	}
	log.Println("Person Not Detected")

	return false

	// if err := os.Remove(imagePath); err != nil {
	// 	log.Printf("error deleting image: %v", err)
	// } else {
	// 	log.Println("image deleted successfully:", imagePath)
	// }
}

// func sendNextToImages(detectedImagePath string) {
// 	queueMutex.Lock()
// 	defer queueMutex.Unlock()
// 	var index int
// 	for i, img := range imageQueue {
// 		if img == detectedImagePath {
// 			index = i
// 			break
// 		}
// 	}
// 	var imagesToSend []string
// 	if index > 0 {
// 		imagesToSend = append(imagesToSend, imageQueue[index-1])
// 	}
// 	imagesToSend = append(imagesToSend, imageQueue[index])
// 	if index < len(imageQueue)-1 {
// 		imagesToSend = append(imagesToSend, imageQueue[index+1])
// 	}
// 	for _, img := range imagesToSend {
// 		err := sendToDiscord(img)
// 		if err != nil {
// 			log.Printf("Error sending to Discord: %v", err)
// 		} else {
// 			fmt.Println("Image sent to Discord successfully.")
// 		}
// 	}
// 	newQueue := make([]string, 0, len(imageQueue))
// 	for i, img := range imageQueue {
// 		if i < index-1 || i > index+1 {
// 			newQueue = append(newQueue, img)
// 		} else {
// 			os.Remove(img)
// 		}
// 	}
// 	imageQueue = newQueue
// }

func sendToDiscord(filename string) error {
	file, err := os.Open("./imagesfile/" + filename)
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


