// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
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

func getOAuthClient(scopes []string) *http.Client {
	data, err := os.ReadFile(getConfig("gdoc-service.json"))
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := google.JWTConfigFromJSON(data, scopes...)
	if err != nil {
		log.Fatal(err)
	}
	return cfg.Client(oauth2.NoContext)
}

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

	srv     *sheets.Service
	docID   string
	sheet   *sheets.Sheet
	whoCell coord
	dateRow rowIndex
	cols    colMap
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

func parseDoc(docID string) *Doc {
	const (
		sheetName = "Proposals"
		fields    = "sheets.properties,sheets.data.rowData.values(effectiveValue,formattedValue)"
	)

	d := new(Doc)

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
			//"https://www.googleapis.com/auth/spreadsheets.readonly",

			// Request write access to update status columns.
			//
			// There's no way to limit this to just one doc! >:(
			"https://www.googleapis.com/auth/spreadsheets",
		}
		client := getOAuthClient(scopes)
		srv, err := sheets.NewService(context.Background(), option.WithHTTPClient(client))
		if err != nil {
			log.Fatalf("Unable to retrieve Docs client: %v", err)
		}
		d.srv = srv

		spreadsheet, err = srv.Spreadsheets.Get(docID).Ranges("'" + sheetName + "'").Fields(fields).Do()
		if err != nil {
			log.Fatalf("Unable to retrieve data from document: %v", err)
		}
		d.docID = docID

		if *debugJSON == "save" {
			js, _ := json.MarshalIndent(spreadsheet, "", "\t")
			js = append(js, '\n')
			os.WriteFile("debug.json", js, 0666)
			os.Exit(0)
		}
	}

	var sheet *sheets.Sheet
	for _, s := range spreadsheet.Sheets {
		if s.Properties.Title == sheetName {
			sheet = s
			break
		}
	}
	if sheet == nil {
		log.Fatalf("did not find %s sheet", sheetName)
	}
	d.sheet = sheet

	var metaCols, cols colMap
	headerRow := rowIndex(-1)
	blank := 0
	meta := true
	for _, data := range sheet.Data {
		for r, row := range data.RowData {
			r := rowIndex(r)
			// On the first row, figure out the meta columns.
			if metaCols == nil {
				var nonempty []colIndex
				for i, val := range row.Values {
					if val.EffectiveValue != nil {
						nonempty = append(nonempty, colIndex(i))
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
				headerRow = r
				d.cols = cols
				continue
			}

			// Process metadata cells
			if meta {
				switch key := metaCols.getString(row, "key"); key {
				case "Who:":
					d.whoCell = coord{d.sheet, metaCols.col("value"), r}
					val := metaCols.getString(row, "value")
					if val == "<WHO>" {
						log.Printf("%s: who list not updated", d.whoCell)
					} else {
						d.Who = regexp.MustCompile(`[,\s]+`).Split(val, -1)
					}
				case "":
					// ignore
				default:
					log.Printf("%s: unknown meta key %q", coord{sheet, metaCols.col("key"), r}, key)
					failure = true
				}
				continue
			}

			// Process second header row.
			if r == headerRow+1 {
				cell := coord{sheet, cols.col("New status"), r}
				val := cols.getEV(row, "New status")
				if val != nil && val.StringValue != nil && *val.StringValue == "<DATE>" {
					log.Printf("%s: date not updated", cell)
					failure = true
				} else {
					date, ok := parseSpreadsheetDate(val)
					if !ok {
						log.Printf("%s: bad date %q", cell, cols.getString(row, "New status"))
						failure = true
						continue
					}
					d.Date = date
				}
				d.dateRow = rowIndex(r)
				continue
			}

			// Process body
			cells := cols.getterString(row)

			var issue Issue
			issue.Minutes = cells("New status")
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
				log.Printf("%s: bad issue number %q", coord{sheet, cols.col("Issue"), r}, num)
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

// FinishDoc performs post-minutes updates to the Doc.
//
// Specifically, it moves the "new status" column to the "current status" column
// and clears the "new status" column and attendees list.
func (d *Doc) FinishDoc() {
	log.Printf("updating status columns in sheet")

	srv := d.srv

	curStatus := d.cols.col("Cur. status")
	newStatus := d.cols.col("New status")

	// Move "new status" to "current status". (CutPasteRequest *almost* works
	// for this, but doesn't seem to implement PASTE_VALUES correctly.)
	copyRange := coord{d.sheet, newStatus, d.dateRow}.To(newStatus+1, -1)
	pasteRange := coord{d.sheet, curStatus, d.dateRow}
	copyReq := &sheets.CopyPasteRequest{
		Source:      copyRange,
		Destination: pasteRange.To(pasteRange.col, pasteRange.row), // Empty means same size as input
		PasteType:   "PASTE_VALUES",
	}
	clearReq := &sheets.UpdateCellsRequest{
		Fields: "userEnteredValue",
		Range:  copyRange,
	}

	// Put placeholder in "new status"
	datePlaceholder := "<DATE>"
	updateDateReq := &sheets.UpdateCellsRequest{
		Fields: "userEnteredValue",
		Start:  coord{d.sheet, newStatus, d.dateRow}.Coord(),
		Rows: []*sheets.RowData{{
			Values: []*sheets.CellData{{
				UserEnteredValue: &sheets.ExtendedValue{
					StringValue: &datePlaceholder,
				},
			}},
		}},
	}

	// Put placeholder in "who"
	whoPlaceholder := "<WHO>"
	updateWhoReq := &sheets.UpdateCellsRequest{
		Fields: "userEnteredValue",
		Start:  d.whoCell.Coord(),
		Rows: []*sheets.RowData{{
			Values: []*sheets.CellData{{
				UserEnteredValue: &sheets.ExtendedValue{
					StringValue: &whoPlaceholder,
				},
			}},
		}},
	}

	// Perform updates
	updateSpreadsheetRequest := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			{CopyPaste: copyReq},
			{UpdateCells: clearReq},
			{UpdateCells: updateDateReq},
			{UpdateCells: updateWhoReq},
		},
	}
	_, err := srv.Spreadsheets.BatchUpdate(d.docID, updateSpreadsheetRequest).Do()
	if err != nil {
		log.Printf("failed to update status columns in sheet: %s", err)
		failure = true
	}
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

type colMap map[string]colIndex

func newColMap(row *sheets.RowData) colMap {
	m := make(map[string]colIndex)
	for i, label := range row.Values {
		if label.EffectiveValue != nil && label.EffectiveValue.StringValue != nil {
			m[*label.EffectiveValue.StringValue] = colIndex(i)
		}
	}
	return m
}

func (m colMap) col(name string) colIndex {
	i, ok := m[name]
	if !ok {
		panic("unknown column label: " + name)
	}
	return i
}

func (m colMap) getEV(row *sheets.RowData, name string) *sheets.ExtendedValue {
	i, ok := m[name]
	if !ok {
		panic("unknown column label: " + name)
	}
	if int(i) >= len(row.Values) {
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

type colIndex int // 0-based

type rowIndex int // 0-based

type coord struct {
	sheet *sheets.Sheet
	col   colIndex
	row   rowIndex
}

func (c coord) String() string {
	return fmt.Sprintf("%s!%c%d", c.sheet.Properties.Title, 'A'+rune(c.col), 1+c.row)
}

func (c coord) Coord() *sheets.GridCoordinate {
	return &sheets.GridCoordinate{
		SheetId:     c.sheet.Properties.SheetId,
		RowIndex:    int64(c.row),
		ColumnIndex: int64(c.col),
	}
}

// To returns a range over columns [c.col, col) and rows [c.row, row).
//
// Col must be >= c.col and row must be >= c.row. In general, they should be
// strictly greater than; if they're equal than this range is empty.
//
// A special value of -1 indicates the range is unbounded in that direction.
func (c coord) To(col colIndex, row rowIndex) *sheets.GridRange {
	// The encoding of this is SO ANNOYING.
	r := &sheets.GridRange{
		SheetId: c.sheet.Properties.SheetId,
	}
	set := func(f *int64, v int64, name string) {
		*f = v
		if v == -1 {
			// Set the field to the zero value and omit it from NullFields so it
			// doesn't get sent.
			*f = 0
			return
		} else if v == 0 {
			// Force send the field, even though it's the zero value.
			r.NullFields = append(r.NullFields, name)
		}
	}
	set(&r.StartRowIndex, int64(c.row), "StartRowIndex")
	set(&r.StartColumnIndex, int64(c.col), "StartColumnIndex")
	set(&r.EndRowIndex, int64(row), "EndRowIndex")
	set(&r.EndColumnIndex, int64(col), "EndColumnIndex")
	return r
}
