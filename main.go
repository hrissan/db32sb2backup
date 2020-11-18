package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"

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

var argv struct {
	inputFile string
	outputFile string
}

func main() {
	log.Println(sq3.Version())

	flag.StringVar(&argv.inputFile, "i", "", "input file to process")
	flag.StringVar(&argv.outputFile, "o", "", "output file to write")

	flag.Parse()

	if argv.inputFile == "" {
		log.Fatalf("-i flag is mandatory")
	}
	if argv.outputFile == "" {
		argv.outputFile = argv.inputFile + ".sb2backup"
	}
	fmt.Printf("Converting %s -> %s\n", argv.inputFile, argv.outputFile)

	_, err := os.Stat(argv.inputFile)
	if os.IsNotExist(err) {
		log.Fatalf("database doesn't exist, %v", err)
	}
	db, err := sql.Open("sqlite3", argv.inputFile)
	if err != nil {
		log.Fatalf("Could not open input file, %v", err)
	}
	defer db.Close()

	var schema, local_version int
	if err := db.QueryRow("select value from DBKeyValue where key = 'schema'").Scan(&schema); err != nil {
		log.Fatalf("DBKeyValue contains no schema, %v", err)
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'local_version'").Scan(&local_version); err != nil {
		log.Fatalf("DBKeyValue contains no local_version, %v", err)
	}
	if schema != 1 {
		log.Fatalf("Unknown schema, must be 1")
	}
	//if local_version > 3 {
	//	log.Fatalf("Unknown local_version, %d", local_version)
	//}

	var backup SB2Backup
	backup.DBVersion = 20

	if err := db.QueryRow("select value from DBKeyValue where key = 'a_token'").Scan(&backup.AToken); err != nil {
		fmt.Printf("DBKeyValue contains no a_token - no cloud sync set up\n")
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'e_key_b64'").Scan(&backup.EKeyB64); err != nil {
		fmt.Printf("DBKeyValue contains no e_key_b64 - no cloud sync set up\n")
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'cypher'").Scan(&backup.Cypher); err != nil {
		fmt.Printf("DBKeyValue contains no cypher - no cloud sync set up\n")
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'selected_sheet'").Scan(&backup.SelectedSheet); err != nil {
		fmt.Printf("DBKeyValue contains no selected_sheet - selected sheet will otnot be restored\n")
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'last_change_id'").Scan(&backup.LastChangeId); err != nil {
		fmt.Printf("DBKeyValue contains no last_change_id - no cloud sync set up\n")
	}
	if err := db.QueryRow("select value from DBKeyValue where key = 'last_change_commands_size'").Scan(&backup.LastChangeCommandSize); err != nil {
		fmt.Printf("DBKeyValue contains no last_change_commands_size - no cloud sync set up\n")
	}

	rows, err := db.Query("select data from DBCommand order by Id")
	if err != nil {
		log.Fatalf("Cannot query DBCommand table, %v", err)
	}
	for rows.Next(){
		var command string
		err := rows.Scan(&command)
		if err != nil{
			log.Fatalf("Error scanning DBCommands, %v", err)
		}
		backup.Commands = append(backup.Commands, Command(command))
	}

	w := bytes.Buffer{}
	if err := json.NewEncoder(&w).Encode(&backup); err != nil {
		log.Fatalf("Error encoding json, %v", err)
	}
	if err := ioutil.WriteFile(argv.outputFile, w.Bytes(), 0644); err != nil {
		log.Fatalf("Error saving file, %v", err)
	}
	log.Printf("Successfully exported %d commands", len(backup.Commands))
	if len(backup.Commands) != 0 {
		log.Printf("Last command is %s", backup.Commands[len(backup.Commands)-1])
	}
}