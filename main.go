package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/caarlos0/env/v11"
	"github.com/phin1x/go-ipp"
	"github.com/unidoc/unipdf/v3/creator"
	"github.com/unidoc/unipdf/v3/model"
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

func (i IppPrinterManager) printPdf(file string) (int, error) {
	// Read the PDF file
	pdfReader, f, err := model.NewPdfReaderFromFile(file, nil)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// Get the number of pages
	numPages, err := pdfReader.GetNumPages()
	if err != nil {
		return 0, err
	}

	// Create a new PDF creator
	c := creator.New()
	page, err := pdfReader.GetPage(1)
	if err != nil {
		return 0, err
	}
	c.SetPageSize(creator.PageSize{page.MediaBox.Width(), page.MediaBox.Height()})

	// Add print info and watermark to the first and last pages
	addPrintInfo := func(page *model.PdfPage, pageNum int) error {
		p := c.NewParagraph(
			fmt.Sprintf(
				`Filename: %s
Printed on: %s
Total pages: %d
`, file))
		p.SetFontSize(10)
		p.SetMargins(10, 10, 10, 10)

		return c.Draw(p)
	}

	fp := c.NewPage()
	_ = addPrintInfo(fp, numPages)
	_ = c.AddPage(fp)

	for p := 1; p <= numPages+1; p++ {
		page, err := pdfReader.GetPage(p)
		if err != nil {
			return 0, err
		}

		// Add the page to the PDF
		c.AddPage(page)
	}

	lp := c.NewPage()
	_ = addPrintInfo(lp, numPages)
	_ = c.AddPage(lp)

	// Write the PDF to a buffer
	var b bytes.Buffer
	if err := c.Write(&b); err != nil {
		return 0, err
	}

	// Print the PDF using IPP
	doc := ipp.Document{
		Name:     file,
		MimeType: "application/pdf",
		Document: &b,
		Size:     b.Len(),
	}

	return i.client.PrintJob(doc, i.printerName, i.defaultJobAttrs)
}

func (i IppPrinterManager) Print(file string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	// if file extension not in list, skip (pdf, png, jpg, jpeg, pwg, pcl)
	if !regexp.MustCompile(`(?i)\.(pdf|png|jpg|jpeg|pwg|pcl)$`).MatchString(file) {
		fmt.Println("file extension not in list, skipping")
		return nil
	}

	var (
		err error
		jId int
	)

	// if file extension is pdf, print it
	if regexp.MustCompile(`(?i)\.pdf$`).MatchString(file) {
		jId, err = i.printPdf(file)
	} else {
		// if file extension is not pdf, convert it to pdf and print it
		jId, err = i.printPdf(strings.Replace(file, filepath.Ext(file), ".pdf", 1))
	}

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
