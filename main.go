package main

import (
	"context"
	"fmt"
	"github.com/caarlos0/env/v11"
	"github.com/fsnotify/fsnotify"
	"github.com/phin1x/go-ipp"
	"golang.org/x/sync/singleflight"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type config struct {
	Port         int    `env:"PORT" envDefault:"3000"`
	IppHost      string `env:"PRINTER_HOST" envDefault:"localhost"`
	IppPort      int    `env:"PRINTER_PORT" envDefault:"631"`
	IppUser      string `env:"PRINTER_USER" envDefault:""`
	IppPass      string `env:"PRINTER_PASS" envDefault:""`
	IppTls       bool   `env:"PRINTER_TLS" envDefault:"false"`
	TppPrinter   string `env:"PRINTER_NAME" envDefault:"Printer"`
	FileRootPath string `env:"FILE_ROOT_PATH" envDefault:"./files"`
}

type IppPrinterManager struct {
	mu          *sync.Mutex
	client      *ipp.IPPClient
	printerName string
	rootFolder  string
	uploadPath  string
	printedPath string
	failedPath  string
}

func (i IppPrinterManager) Print(file string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if fi, err := os.Stat(file); err == nil && fi.IsDir() {
		fmt.Println("is dir, skipping")
		return nil
	}

	// if file extension not in list, skip (pdf, png, jpg, jpeg, pwg, pcl)
	if !regexp.MustCompile(`(?i)\.(pdf|png|jpg|jpeg|pwg|pcl)$`).MatchString(file) {
		fmt.Println("file extension not in list, skipping")
		return nil
	}

	var err error
	jId := 0
	if jId, err = i.client.PrintFile(file, i.printerName, map[string]interface{}{}); err != nil {
		os.Rename(file, strings.Replace(file, "/upload/", fmt.Sprintf("/failed/%s/", time.Now().Format("2006-01-02")), 1))
		return err
	}

	fmt.Printf("Printed %s\n", file)

	newFile := strings.Replace(file, "/upload/", fmt.Sprintf("/printed/%s_%d_", time.Now().Format("2006-01-02"), jId), 1)
	if err := os.Rename(file, newFile); err != nil {
		return err
	}

	fmt.Printf("Moved to %s\n", newFile)

	return nil
}

func (i IppPrinterManager) WatchFiles(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	var group singleflight.Group

	// Start listening for events.
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				log.Printf("new file: %+v\n", event)

				if event.Has(fsnotify.Write) {
					group.Do(event.Name, func() (interface{}, error) {
						time.Sleep(3 * time.Second)
						return nil, i.Print(event.Name)
					})
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			}
		}
	}()

	// Add a path.
	err = watcher.Add(i.uploadPath)
	if err != nil {
		return err
	}

	// Block main goroutine forever.
	<-ctx.Done()

	return nil

}

func NewIppPrinterManager(client *ipp.IPPClient, printerName, rootFolder string) (*IppPrinterManager, error) {
	ipm := &IppPrinterManager{
		client:      client,
		printerName: printerName,

		mu: &sync.Mutex{},

		rootFolder:  rootFolder,
		uploadPath:  fmt.Sprintf("%s/upload", rootFolder),
		printedPath: fmt.Sprintf("%s/printed", rootFolder),
		failedPath:  fmt.Sprintf("%s/failed", rootFolder),
	}

	if err := os.MkdirAll(ipm.uploadPath, 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(ipm.printedPath, 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(ipm.failedPath, 0755); err != nil {
		return nil, err
	}

	if err := filepath.Walk(ipm.uploadPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			ipm.Print(path)
		}

		return nil

	}); err != nil {
		return nil, err
	}

	return ipm, nil
}

func main() {
	cfg, err := env.ParseAs[config]()
	if err != nil {
		fmt.Printf("%+v\n", err)
	}

	client := ipp.NewIPPClient(cfg.IppHost, cfg.IppPort, cfg.IppUser, cfg.IppPass, cfg.IppTls)

	ipm, err := NewIppPrinterManager(client, cfg.TppPrinter, cfg.FileRootPath)
	if err != nil {
		log.Fatal(err)
	}

	if err := ipm.WatchFiles(context.Background()); err != nil {
		log.Fatal(err)
	}
}
