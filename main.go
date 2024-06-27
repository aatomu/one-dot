package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aatomu/aatomlib/utils"
)

var (
	Listen          = ":1036"
	ColorList       = []string{}
	ColorListRaw    []byte
	Users           = map[string]time.Time{}
	Interval        = 1 * time.Minute
	HistoryInterval = 2 * time.Hour
	Canvas          []byte
)

func main() {
	// 移動
	_, file, _, _ := runtime.Caller(0)
	goDir := filepath.Dir(file) + "/"
	os.Chdir(goDir)

	// Read Color List
	bytes, err := os.ReadFile("color_list.json")
	if err != nil {
		log.Printf("[Error] failed read color list:%s\n", err)
		return
	}
	json.Unmarshal(bytes, &ColorList)
	ColorListRaw = bytes

	// Read Canvas
	history_list := []string{}
	filepath.Walk("history", func(path string, info fs.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".png" {
			return nil
		}

		history_list = append(history_list, path)
		return nil
	})
	sort.Slice(history_list, func(i, j int) bool {
		i_file := filepath.Base(history_list[i])
		i_file = strings.TrimSuffix(i_file, ".png")
		i_time, _ := time.Parse("20060102-15_04_05", i_file)
		j_file := filepath.Base(history_list[j])
		j_file = strings.TrimSuffix(j_file, ".png")
		j_time, _ := time.Parse("20060102-15_04_05", j_file)
		return i_time.Unix() > j_time.Unix()
	})

	log.Printf("[INFO] history list (newest 10files)\n")
	for index, canvas := range history_list {
		if index > 10 {
			break
		}
		log.Printf("  %s\n", filepath.Base(canvas))
	}
	bytes, err = os.ReadFile(history_list[0])
	if err != nil {
		log.Printf("[Error] failed read newest canvas:%s\n", err)
		return
	}
	Canvas = bytes

	// アクセス先
	http.HandleFunc("/", HttpResponse)
	// Web鯖 起動
	go func() {
		log.Printf("[INFO] http server boot\n")
		err := http.ListenAndServe(Listen, nil)
		if err != nil {
			log.Printf("[ERROR] http server boot failed: %s\n", err)
			return
		}
	}()

	// Save
	go func() {
		ticker := time.NewTicker(HistoryInterval)
		for {
			<-ticker.C
			SaveCanvas()
		}
	}()
	defer SaveCanvas()

	<-utils.BreakSignal()
}

// ページ表示
func HttpResponse(w http.ResponseWriter, r *http.Request) {
	var IP string
	if strings.Contains(r.RemoteAddr, "127.0.0.1") {
		IP = r.Header.Get("Cf-Connecting-Ip")
	} else {
		IP = strings.SplitN(r.RemoteAddr, ":", 2)[0]
	}

	switch r.URL.Path {
	case "/":
		log.Printf("[INFO] access: IP:%s URI:%s\n", IP, r.RequestURI)
		bytes, _ := os.ReadFile("index.html")
		w.Write(bytes)
	case "/canvas.png":
		w.Write(Canvas)
	case "/color_list.json":
		log.Printf("[INFO] access: IP:%s URI:%s\n", IP, r.RequestURI)
		w.Write(ColorListRaw)

	case "/interval":
		now := time.Now()
		if write, ok := Users[IP]; ok {
			if now.Before(write) {
				w.Write([]byte(fmt.Sprintf("%d", write.UnixMilli())))
				return
			}
		}
		w.Write([]byte(""))

	case "/place":
		now := time.Now()
		if write, ok := Users[IP]; ok {
			if now.Before(write) {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
		}
		log.Printf("[INFO] access: IP:%s URI:%s\n", IP, r.RequestURI)

		X, err := strconv.Atoi(r.URL.Query().Get("x"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing arguments"))
			return
		}
		Y, err := strconv.Atoi(r.URL.Query().Get("y"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing arguments"))
			return
		}
		index, err := strconv.Atoi(r.URL.Query().Get("index"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing arguments"))
			return
		}
		if index > len(ColorList) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Invalid arguments"))
			return
		}

		// decode canvas
		img, _, err := image.Decode(bytes.NewReader(Canvas))
		if err != nil {
			log.Printf("[ERROR] decode image error: %s\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// modify canvas
		imgBounds := img.Bounds()
		canvas := image.NewRGBA(image.Rect(0, 0, imgBounds.Dx(), imgBounds.Dy()))
		draw.Draw(canvas, canvas.Bounds(), img, imgBounds.Min, draw.Src)
		canvas.SetRGBA(X, Y, ParseHexColor(ColorList[index]))

		// encode canvas
		var buf bytes.Buffer
		err = png.Encode(&buf, canvas)
		if err != nil {
			log.Printf("[ERROR] encode image error: %s\n", err)
			w.WriteHeader(http.StatusInternalServerError)
		}
		Canvas, _ = io.ReadAll(&buf)

		// return
		unlock := now.Add(Interval)
		Users[IP] = unlock
		w.WriteHeader(http.StatusOK)

	default:
		log.Printf("[WARN] access: IP:%s URI:%s", IP, r.RequestURI)
		w.WriteHeader(http.StatusNotFound)
	}
}

func ParseHexColor(s string) (c color.RGBA) {
	c.A = 0xff
	fmt.Sscanf(s, "#%02x%02x%02x", &c.R, &c.G, &c.B)
	return
}

func SaveCanvas() {
	if _, err := os.Stat("history"); err != nil {
		err := os.Mkdir("history", 0755)
		if err != nil {
			log.Printf("[ERROR] create \"history\" dir: %s\n", err)
			return
		}
	}

	history := time.Now().Format("20060102-15_04_05") + ".png"
	f, err := os.Create(filepath.Join(".", "history", history))
	if err != nil {
		log.Printf("[ERROR] create \"history\" file %s: %s\n", history, err)
		return
	}
	defer f.Close()

	f.Write(Canvas)
	log.Printf("[INFO] canvas has saved :%s \n", history)
}
