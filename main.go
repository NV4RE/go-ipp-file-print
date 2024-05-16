package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/caarlos0/env/v11"
	"github.com/phin1x/go-ipp"
	"log"
	"maps"
	"os"
	"path"
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
	IppPrinter   string `env:"PRINTER_NAME" envDefault:"Printer"`
	IppJobAttrs  string `env:"PRINTER_JOB_ATTRS" envDefault:"{}"`
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

	defaultJobAttrs map[string]any
}

//go:embed img.png
var img []byte

func (i IppPrinterManager) Print(file string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	// if file extension not in list, skip (pdf, png, jpg, jpeg, pwg, pcl)
	if !regexp.MustCompile(`(?i)\.(pdf|png|jpg|jpeg|pwg|pcl)$`).MatchString(file) {
		fmt.Println("file extension not in list, skipping")
		return nil
	}

	fileStats, err := os.Stat(file)
	if err != nil {
		return err
	}

	fileName := path.Base(file)
	document, err := os.Open(file)
	if err != nil {
		return err
	}
	defer document.Close()

	ja := map[string]any{
		ipp.AttributeJobName: fileName,
	}
	maps.Copy(ja, i.defaultJobAttrs)

	docs := []ipp.Document{
		{
			Document: document,
			Name:     fileName,
			Size:     int(fileStats.Size()),
			MimeType: ipp.MimeTypeOctetStream,
		},
		{
			Document: strings.NewReader(string(img)),
			Name:     "img.png",
			Size:     len(img),
			MimeType: ipp.MimeTypeOctetStream,
		},
	}

	jId, err := i.client.PrintDocuments(docs, i.printerName, ja)
	if err != nil {
		os.Rename(file, strings.Replace(file, "/upload/", fmt.Sprintf("/failed/%s_", time.Now().Format("2006-01-02")), 1))
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

		if info.IsDir() {
			return nil
		}

		time.Sleep(3 * time.Second)
		if err := i.Print(path); err != nil {
			log.Printf("Failed to print %s: %s\n", path, err)
		}

		return nil

	})
}

func NewIppPrinterManager(client *ipp.IPPClient, printerName, rootFolder string, jobAttr map[string]any) (*IppPrinterManager, error) {
	ipm := &IppPrinterManager{
		client:          client,
		printerName:     printerName,
		defaultJobAttrs: jobAttr,

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

	jobAttrs := make(map[string]any)
	if err := json.Unmarshal([]byte(cfg.IppJobAttrs), &jobAttrs); err != nil {
		log.Printf("Failed to parse job attributes: %s\n", err)
	}

	ipm, err := NewIppPrinterManager(client, cfg.IppPrinter, cfg.FileRootPath, jobAttrs)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Starting file watcher")

	if err := ipm.WatchFiles(context.Background()); err != nil {
		log.Fatal(err)
	}
}
