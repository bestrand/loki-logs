package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type LokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

type LokiRequest struct {
	Streams []LokiStream `json:"streams"`
}

func main() {
	lokiURL := os.Getenv("LOKI_URL")
	if lokiURL == "" {
		lokiURL = "http://localhost:3100"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	go func() {
		time.Sleep(5 * time.Second)
		importSampleLogs(lokiURL)
	}()

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, "./static/index.html")
	})

	http.HandleFunc("/api/import-text", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
			return
		}

		logText := r.FormValue("logText")
		if logText == "" {
			http.Error(w, "No log text provided", http.StatusBadRequest)
			return
		}

		lines := strings.Split(logText, "\n")
		var validLines []string
		var serviceName string

		if len(lines) > 0 {
			firstLine := strings.TrimSpace(lines[0])
			if strings.HasPrefix(firstLine, "service_name:") {
				parts := strings.SplitN(firstLine, ":", 2)
				if len(parts) == 2 {
					serviceName = strings.TrimSpace(parts[1])
				}
				lines = lines[1:]
			}
		}

		if serviceName == "" {
			http.Error(w, "Missing service_name. First line must be 'service_name: your-service-name'", http.StatusBadRequest)
			return
		}

		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				validLines = append(validLines, strings.TrimSpace(line))
			}
		}

		if len(validLines) == 0 {
			http.Error(w, "No valid log lines found", http.StatusBadRequest)
			return
		}

		if err := sendLogsToLoki(lokiURL, validLines, serviceName); err != nil {
			http.Error(w, fmt.Sprintf("Failed to send logs to Loki: %v", err), http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "Successfully imported %d log lines as service '%s'", len(validLines), serviceName)
	})

	http.HandleFunc("/api/upload-files", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
			return
		}

		err := r.ParseMultipartForm(32 << 20)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to parse form: %v", err), http.StatusBadRequest)
			return
		}

		files := r.MultipartForm.File["files"]
		if len(files) == 0 {
			http.Error(w, "No files provided", http.StatusBadRequest)
			return
		}

		var results []string
		var errors []string
		totalLines := 0

		for _, fileHeader := range files {
			file, err := fileHeader.Open()
			if err != nil {
				errors = append(errors, fmt.Sprintf("Failed to open %s: %v", fileHeader.Filename, err))
				continue
			}

			lines, serviceName, err := readFromFileWithServiceName(file)
			file.Close()
			if err != nil {
				errors = append(errors, fmt.Sprintf("Failed to read %s: %v", fileHeader.Filename, err))
				continue
			}

			if serviceName == "" {
				errors = append(errors, fmt.Sprintf("ERROR: %s is missing service_name. Add 'service_name: your-service-name' as the first line", fileHeader.Filename))
				continue
			}

			if len(lines) == 0 {
				errors = append(errors, fmt.Sprintf("ERROR: %s contains no valid log lines", fileHeader.Filename))
				continue
			}

			if err := sendLogsToLoki(lokiURL, lines, serviceName); err != nil {
				errors = append(errors, fmt.Sprintf("Failed to import %s: %v", fileHeader.Filename, err))
				continue
			}

			results = append(results, fmt.Sprintf("✓ %s: %d lines imported (service: %s)", fileHeader.Filename, len(lines), serviceName))
			totalLines += len(lines)
		}

		if len(errors) > 0 && len(results) == 0 {
			http.Error(w, strings.Join(errors, "\n"), http.StatusBadRequest)
			return
		}

		response := fmt.Sprintf("Processed %d file(s), imported %d total lines\n\n", len(files), totalLines)
		if len(results) > 0 {
			response += "SUCCESS:\n" + strings.Join(results, "\n")
		}
		if len(errors) > 0 {
			response += "\n\nERRORS:\n" + strings.Join(errors, "\n")
		}

		fmt.Fprint(w, response)
	})

	log.Printf("Log importer UI starting on http://0.0.0.0:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func readFromFileWithServiceName(file multipart.File) ([]string, string, error) {
	var lines []string
	var serviceName string
	scanner := bufio.NewScanner(file)

	if scanner.Scan() {
		firstLine := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(firstLine, "service_name:") {
			parts := strings.SplitN(firstLine, ":", 2)
			if len(parts) == 2 {
				serviceName = strings.TrimSpace(parts[1])
			}
		} else {
			if firstLine != "" {
				lines = append(lines, firstLine)
			}
		}
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}

	return lines, serviceName, scanner.Err()
}

func sendLogsToLoki(lokiURL string, lines []string, job string) error {
	if len(lines) == 0 {
		return nil
	}

	var values [][]string
	now := time.Now()

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		timestamp := strconv.FormatInt(now.Add(time.Duration(i)*time.Microsecond).UnixNano(), 10)
		values = append(values, []string{timestamp, line})
	}

	if len(values) == 0 {
		return nil
	}

	request := LokiRequest{
		Streams: []LokiStream{
			{
				Stream: map[string]string{
					"job":          job,
					"source":       "ui-import",
					"service_name": job,
				},
				Values: values,
			},
		},
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %v", err)
	}

	resp, err := http.Post(lokiURL+"/loki/api/v1/push", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to send to Loki: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Loki returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func importSampleLogs(lokiURL string) {
	sampleLogs := []string{
		"2024-06-24 09:00:00 INFO Application startup initiated (example log)",
		"2024-06-24 09:00:01 INFO Loading configuration from config.yml (example log)",
		"2024-06-24 09:00:02 INFO Database connection pool initialized (max: 10) (example log)",
		"2024-06-24 09:00:03 INFO Starting HTTP server on port 8080 (example log)",
		"2024-06-24 09:00:04 INFO Authentication module loaded successfully (example log)",
		"2024-06-24 09:00:05 WARN High memory usage detected: 85% (example log)",
		"2024-06-24 09:00:06 INFO User session cache initialized (example log)",
		"2024-06-24 09:00:07 DEBUG Loading middleware: CORS, Auth, Logging (example log)",
		"2024-06-24 09:00:08 INFO Background job scheduler started (example log)",
		"2024-06-24 09:00:09 ERROR Failed to connect to external API: timeout (example log)",
		"2024-06-24 09:00:10 INFO Retrying external API connection... (example log)",
		"2024-06-24 09:00:11 INFO External API connection established (example log)",
		"2024-06-24 09:00:12 INFO Application ready to accept requests (example log)",
		"2024-06-24 09:00:13 INFO Health check endpoint active: /health (example log)",
		"2024-06-24 09:00:14 INFO Metrics endpoint active: /metrics (example log)",
	}

	log.Println("Importing sample logs to verify setup...")
	if err := sendLogsToLoki(lokiURL, sampleLogs, "sample-app"); err != nil {
		log.Printf("Failed to import sample logs: %v", err)
	} else {
		log.Printf("Successfully imported %d sample logs", len(sampleLogs))
	}
}
