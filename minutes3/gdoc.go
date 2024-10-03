// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

func getClient() *http.Client {
	data, err := os.ReadFile("/Users/rsc/.cred/proposal-minutes-gdoc.json")
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := google.JWTConfigFromJSON(data, "https://www.googleapis.com/auth/spreadsheets")
	if err != nil {
		log.Fatal(err)
	}
	return cfg.Client(oauth2.NoContext)
}

type Doc struct {
	Date   time.Time
	Text   []string // top-level text
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
		client := getClient()

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

	const (
		column        = -'A'
		issueColumn   = column + 'A'
		statusColumn  = column + 'B'
		titleColumn   = column + 'D'
		detailsColumn = column + 'E'

		metaColumn      = column + 'B'
		metaValueColumn = column + 'D'

		maxColumn = column + 'E'
	)
	blank := 0
	meta := true
	for _, data := range sheet.Data {
		for r, row := range data.RowData {
			cells := make([]string, max(len(row.Values), maxColumn+1))
			for c, cell := range row.Values {
				v := cell.EffectiveValue
				if v != nil && v.StringValue != nil {
					cells[c] = *v.StringValue
				}
			}
			if cells[issueColumn] == "Issue" {
				meta = false
				continue
			}
			if meta {
				val := cells[metaValueColumn]
				switch cells[metaColumn] {
				case "Date:":
					var day int
					if len(row.Values) > metaValueColumn {
						v := row.Values[metaValueColumn].EffectiveValue
						if v != nil && v.NumberValue != nil {
							day = int(*v.NumberValue)
						}
					}
					if day == 0 {
						log.Printf("%c%d: bad date %q", metaValueColumn-column, r+1, val)
						failure = true
						continue
					}
					var day0 = time.Date(1899, time.December, 30, 12, 0, 0, 0, time.UTC)
					d.Date = day0.Add(time.Duration(time.Duration(day) * 24 * time.Hour))
				case "Who:":
					d.Who = regexp.MustCompile(`[,\s]+`).Split(val, -1)
				case "":
					// ignore
				default:
					log.Printf("%c%d: unknown meta key %q", metaColumn-column, r+1, cells[metaColumn])
					failure = true
				}
				continue
			}

			var issue Issue
			issue.Minutes = cells[statusColumn]
			issue.Title = cells[titleColumn]
			issue.Details = cells[detailsColumn]
			num := cells[issueColumn]
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
				log.Printf("%c%d: bad issue number %q", issueColumn-column, r+1, num)
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
