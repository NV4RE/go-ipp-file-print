package main

import (
	"context"
	"fmt"
	"github.com/caarlos0/env/v11"
	"github.com/phin1x/go-ipp"
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
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := i.PrintAll(); err != nil {
				log.Println(err)
			}
			time.Sleep(1 * time.Second)
		}

	}
}

func (i IppPrinterManager) PrintAll() error {
	return filepath.Walk(i.uploadPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			i.Print(path)
		}

		return nil

	})
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
