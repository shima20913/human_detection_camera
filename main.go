package main

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
