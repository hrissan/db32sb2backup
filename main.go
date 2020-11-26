package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"

	sq3 "github.com/mattn/go-sqlite3"
)

type Command string

func (c *Command) MarshalJSON() ([]byte, error) {
	// Commands are strings in json format, will copy as is to json
	return []byte(*c), nil
}

type SB2Backup struct {
	DBVersion int `json:"db_version"`

	AToken string `json:"a_token,omitempty"`
	EKeyB64 string `json:"e_key_b64,omitempty"`
	Cypher string  `json:"cypher,omitempty"`

	SelectedSheet string  `json:"selected_sheet,omitempty"`

	LastChangeId int  `json:"last_change_id,omitempty"`
	LastChangeCommandSize int  `json:"last_change_commands_size,omitempty"`

	AppVersionInfo string `json:"app_version_info"`

	Commands []Command `json:"commands"`
}

func convertDB3File(inputFile string, verbose bool) (*SB2Backup, *bytes.Buffer, error) {

	_, err := os.Stat(inputFile)
	if os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("database doesn't exist, %v", err)
	}
	db, err := sql.Open("sqlite3", inputFile)
	if err != nil {
		return nil, nil, fmt.Errorf("sqlite3 could not open input file, %v", err)
	}
	defer db.Close()

	var schema, local_version int
	if err := db.QueryRow("select value from DBKeyValue where key = 'schema'").Scan(&schema); err != nil {
		return nil, nil, fmt.Errorf("DBKeyValue contains no schema, %v", err)
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'local_version'").Scan(&local_version); err != nil {
		return nil, nil, fmt.Errorf("DBKeyValue contains no local_version, %v", err)
	}
	if schema != 1 {
		return nil, nil, fmt.Errorf("Unknown schema, must be 1")
	}
	//if local_version > 3 {
	//	log.Fatalf("Unknown local_version, %d", local_version)
	//}

	var backup SB2Backup
	backup.DBVersion = 20

	if err := db.QueryRow("select value from DBKeyValue where key = 'a_token'").Scan(&backup.AToken); err != nil {
		if verbose {
			fmt.Printf("DBKeyValue contains no a_token - no cloud sync set up\n")
		}
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'e_key_b64'").Scan(&backup.EKeyB64); err != nil {
		if verbose {
			fmt.Printf("DBKeyValue contains no e_key_b64 - no cloud sync set up\n")
		}
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'cypher'").Scan(&backup.Cypher); err != nil {
		if verbose {
			fmt.Printf("DBKeyValue contains no cypher - no cloud sync set up\n")
		}
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'selected_sheet'").Scan(&backup.SelectedSheet); err != nil {
		if verbose {
			fmt.Printf("DBKeyValue contains no selected_sheet - selected sheet will not be restored\n")
		}
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'last_change_id'").Scan(&backup.LastChangeId); err != nil {
		if verbose {
			fmt.Printf("DBKeyValue contains no last_change_id - no cloud sync set up\n")
		}
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'last_change_commands_size'").Scan(&backup.LastChangeCommandSize); err != nil {
		if verbose {
			fmt.Printf("DBKeyValue contains no last_change_commands_size - no cloud sync set up\n")
		}
	}

	rows, err := db.Query("select data from DBCommand order by Id")
	if err != nil {
		return nil, nil, fmt.Errorf("Cannot query DBCommand table, %v", err)
	}
	for rows.Next(){
		var command string
		err := rows.Scan(&command)
		if err != nil{
			_ = rows.Close()
			return nil, nil, fmt.Errorf("Error scanning DBCommands, %v", err)
		}
		backup.Commands = append(backup.Commands, Command(command))
	}

	jw := bytes.Buffer{}
	if err := json.NewEncoder(&jw).Encode(&backup); err != nil {
		return nil, nil, fmt.Errorf("Error encoding json, %v", err)
	}
	return &backup, &jw, nil
}

var argv struct {
	inputFile string
	outputFile string
	webAppPort int
}

func writeError(w http.ResponseWriter, error string) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(error))
}

func uploadFile(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(50 << 20)
	if err != nil {
		writeError(w, fmt.Sprintf("Not a multipart form, %v", err))
		return
	}
	file, handler, err := r.FormFile("db3File")
	if err != nil {
		writeError(w, fmt.Sprintf("Error Retrieving the File, %v", err))
		return
	}
	defer file.Close()
	log.Printf("Uploaded file: '%s', size: %v", handler.Filename, handler.Size)

	// Create a temporary file within our temp-images directory that follows
	// a particular naming pattern
	tempFile, err := ioutil.TempFile("tmp", "upload-*.db3")
	if err != nil {
		log.Printf("    Cannot create tmp file, %v", err)
		writeError(w, fmt.Sprintf("Cannot create tmp file, %v", err))
		return
	}
	tempName := tempFile.Name()
	// Erase user's secrets. Alas, sqlite will not easily open memory chunk as a DB without temp file
	defer os.Remove(tempName)

	_, err = io.Copy(tempFile, file)
	if err != nil {
		log.Printf("    Error saving tmp file, %v", err)
		writeError(w, fmt.Sprintf("Error saving tmp file, %v", err))
		_ = tempFile.Close()
		return
	}
	_ = tempFile.Close()
	backup, jw, err := convertDB3File(tempName, false)
	if err != nil {
		log.Printf("    Error converting file, %v", err)
		writeError(w, fmt.Sprintf("Error converting file %v", err))
		return;
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(handler.Filename + ".sb2backup"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", jw.Len()))
	log.Printf("    Converted file, size: %v, #commands: %v, cypher: '%s'", jw.Len(), len(backup.Commands), backup.Cypher)
	_, _ = w.Write(jw.Bytes())
}

func indexFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
  <head>
    <title>Convert Smart Budget 2 internal database to backup file that can be opened in Smart Budget 2 again</title>
  </head>
  <body>
	Convert Smart Budget 2 internal database to backup file that can be opened in Smart Budget 2 again<br/>
    <form
      enctype="multipart/form-data"
      action="upload.html"
      method="post"
    >
      <input type="file" name="db3File" />
      <input type="submit" value="Convert" />
    </form><br/>
	<a href="https://github.com/hrissan/db32sb2backup">Source code</a>
  </body>
</html>`))
}

func main() {
	log.Println(sq3.Version())

	flag.IntVar(&argv.webAppPort, "port", 0, "run web server on selected port, 0 to run as a command-line tool")
	flag.StringVar(&argv.inputFile, "i", "", "input file to process")
	flag.StringVar(&argv.outputFile, "o", "", "output file to write")

	flag.Parse()

	if argv.webAppPort != 0 {
		tempFile, err := ioutil.TempFile("tmp", "upload-*.db3")
		if err != nil {
			log.Fatalf("Cannot create tmp file, folder 'tmp' should exist in the current path, %v", err)
			return
		}
		tempName := tempFile.Name()
		_ = tempFile.Close()
		_ = os.Remove(tempName)

		f, err := os.OpenFile("log.txt", os.O_RDWR | os.O_CREATE | os.O_APPEND, 0666)
		if err != nil {
			log.Fatalf("Error opening log file: %v", err)
		}
		defer f.Close()

		log.SetOutput(io.MultiWriter(os.Stdout, f))
		log.Printf("Start")

		http.HandleFunc("/upload.html", uploadFile)
		http.HandleFunc("/index.html", indexFile)
		http.HandleFunc("/", indexFile)
		if err:= http.ListenAndServe(fmt.Sprintf(":%d", argv.webAppPort), nil); err != nil {
			log.Fatal("Cannot listen on selected port, %v", err)
		}
		return
	}

	if argv.inputFile == "" {
		log.Fatalf("-i flag is mandatory")
	}
	if argv.outputFile == "" {
		argv.outputFile = argv.inputFile + ".sb2backup"
	}
	fmt.Printf("Converting %s -> %s\n", argv.inputFile, argv.outputFile)
	backup, jw, err := convertDB3File(argv.inputFile, true)
	if err != nil {
		log.Fatalf("%v", err)
	}
	if err := ioutil.WriteFile(argv.outputFile, jw.Bytes(), 0644); err != nil {
		log.Fatalf("Error saving file, %v", err)
	}
	log.Printf("Successfully exported %d commands", len(backup.Commands))
	if len(backup.Commands) != 0 {
		log.Printf("Last command is %s", backup.Commands[len(backup.Commands)-1])
	}
}