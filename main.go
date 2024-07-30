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

func main() {

	godotenv.Load(".env")
	sendToDiscord(".env")

	http.HandleFunc("/upload", uploadHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
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

	response, err := detectObjects("http://deepdetect_server_url/predict", imagePath)
	if err != nil {
		log.Printf("Error detecting objects: %v", err)
		return
	}

	processPredictions(response, imagePath)

}

func detectObjects(url, imagePath string) (*Response, error) {
	client := resty.New()

	fileBytes, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, fmt.Errorf("could not read image file: %w", err)
	}

	resp, err := client.R().
		SetFileReader("image", filepath.Base(imagePath), bytes.NewReader(fileBytes)).
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

func processPredictions(response *Response, imagePath string) {
	for _, prediction := range response.Body.Predictions {
		for _, class := range prediction.Classes {
			if class.Cat == "Person" {
				err := sendToDiscord(imagePath)
				if err != nil {
					log.Printf("Error sending to Discord: %v", err)
				} else {
					fmt.Println("Image sent to Discord successfully.")
				}
				return
			}
		}
	}
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
	_, err = client.Do(req)
	return err
}
