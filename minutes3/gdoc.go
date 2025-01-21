// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"log"
	"os"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

func getOAuthConfig(scopes []string) *oauth2.Config {
	// Read the "client" (application) config.
	data, err := os.ReadFile(getConfig("gdoc.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Println("Please follow the instructions in README.md to create a GCP OAuth client ID.")
		}
		log.Fatal(err)
	}
	config, err := google.ConfigFromJSON(data, scopes...)
	if err != nil {
		log.Fatalf("unable to parse client secret file to config: %v", err)
	}
	return config
}

type Doc struct {
	Date   time.Time
	Who    []string
	Issues []*Issue
}

type Issue struct {
	Number  int
	Title   string
	Details string
	Minutes string
	Comment string
	Notes   string
}

var (
	debugJSON = flag.String("debugjson", "", "json debug mode (save, load)")
)

func parseDoc() *Doc {
	var spreadsheet *sheets.Spreadsheet
	if *debugJSON == "load" {
		spreadsheet = new(sheets.Spreadsheet)
		data, err := os.ReadFile("debug.json")
		if err != nil {
			log.Fatal(err)
		}
		if err := json.Unmarshal(data, spreadsheet); err != nil {
			log.Fatal(err)
		}
	} else {
		scopes := []string{
			"https://www.googleapis.com/auth/spreadsheets.readonly",
		}
		config := getOAuthConfig(scopes)
		client := makeOAuthClient(getCacheDir(), config)
		srv, err := sheets.NewService(context.Background(), option.WithHTTPClient(client))
		if err != nil {
			log.Fatalf("Unable to retrieve Docs client: %v", err)
		}

		id := "1EG7oPcLls9HI_exlHLYuwk2YaN4P5mDc4O2vGyRqZHU"

		spreadsheet, err = srv.Spreadsheets.Get(id).IncludeGridData(true).Do()
		if err != nil {
			log.Fatalf("Unable to retrieve data from document: %v", err)
		}

		if *debugJSON == "save" {
			js, _ := json.MarshalIndent(spreadsheet, "", "\t")
			js = append(js, '\n')
			os.WriteFile("debug.json", js, 0666)
			os.Exit(0)
		}
	}

	d := new(Doc)
	var sheet *sheets.Sheet
	for _, s := range spreadsheet.Sheets {
		if s.Properties.Title == "Proposals" {
			sheet = s
			break
		}
	}
	if sheet == nil {
		log.Fatal("did not find Proposals sheet")
	}

	var metaCols, cols colMap
	blank := 0
	meta := true
	for _, data := range sheet.Data {
		for r, row := range data.RowData {
			// On the first row, figure out the meta columns.
			if metaCols == nil {
				var nonempty []int
				for i, val := range row.Values {
					if val.EffectiveValue != nil {
						nonempty = append(nonempty, i)
					}
				}
				if len(nonempty) != 2 {
					log.Fatalf("on first spreadsheet row, expected two non-empty cells, got %d", len(nonempty))
				}
				metaCols = colMap{"0": 0, "key": nonempty[0], "value": nonempty[1]}
			}
			// Should we switch to the body?
			if meta && metaCols.getString(row, "0") == "Issue" {
				meta = false
				cols = newColMap(row)
				continue
			}

			// Process metadata cells
			if meta {
				switch key := metaCols.getString(row, "key"); key {
				case "Date:":
					date, ok := parseSpreadsheetDate(metaCols.getEV(row, "value"))
					if !ok {
						log.Printf("%c%d: bad date %q", metaCols.col("value"), r+1, metaCols.getString(row, "value"))
						failure = true
						continue
					}
					d.Date = date
				case "Who:":
					d.Who = regexp.MustCompile(`[,\s]+`).Split(metaCols.getString(row, "value"), -1)
				case "":
					// ignore
				default:
					log.Printf("%c%d: unknown meta key %q", metaCols.col("key"), r+1, key)
					failure = true
				}
				continue
			}

			// Process body
			cells := cols.getterString(row)

			var issue Issue
			issue.Minutes = cells("Status")
			issue.Title = cells("Title")
			issue.Details = cells("Proposal Details")
			num := cells("Issue")
			if num == "" && issue == (Issue{}) {
				blank++
				continue
			}
			if blank > 10 {
				log.Printf("found stray non-empty row %d", r+1)
				failure = true
			}
			n, err := strconv.Atoi(num)
			if err != nil {
				log.Printf("%c%d: bad issue number %q", cols.col("Issue"), r+1, num)
				failure = true
				continue
			}
			issue.Number = n
			d.Issues = append(d.Issues, &issue)
		}
	}

	if d.Date.IsZero() {
		log.Printf("spreadsheet Date: missing")
		failure = true
	} else if time.Since(d.Date) > 5*24*time.Hour || -time.Since(d.Date) > 24*time.Hour {
		log.Printf("spreadsheet Date: too old")
		failure = true
	}

	return d
}

func parseSpreadsheetDate(cell *sheets.ExtendedValue) (time.Time, bool) {
	if cell == nil || cell.NumberValue == nil {
		return time.Time{}, false
	}

	day := int(*cell.NumberValue)
	if day == 0 {
		return time.Time{}, false
	}
	var day0 = time.Date(1899, time.December, 30, 12, 0, 0, 0, time.UTC)
	return day0.Add(time.Duration(time.Duration(day) * 24 * time.Hour)), true
}

type colMap map[string]int

func newColMap(row *sheets.RowData) colMap {
	m := make(map[string]int)
	for i, label := range row.Values {
		if label.EffectiveValue != nil && label.EffectiveValue.StringValue != nil {
			m[*label.EffectiveValue.StringValue] = i
		}
	}
	return m
}

func (m colMap) col(name string) rune {
	i, ok := m[name]
	if !ok {
		panic("unknown column label: " + name)
	}
	return 'A' + rune(i)
}

func (m colMap) getEV(row *sheets.RowData, name string) *sheets.ExtendedValue {
	i, ok := m[name]
	if !ok {
		panic("unknown column label: " + name)
	}
	if i >= len(row.Values) {
		return nil
	}
	return row.Values[i].EffectiveValue
}

func (m colMap) getString(row *sheets.RowData, name string) string {
	v := m.getEV(row, name)
	if v == nil || v.StringValue == nil {
		return ""
	}
	return *v.StringValue
}

func (m colMap) getterString(row *sheets.RowData) func(name string) string {
	return func(name string) string {
		return m.getString(row, name)
	}
}
