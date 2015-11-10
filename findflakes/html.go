// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"html/template"
	"io"
	"log"
	"reflect"
)

// TODO: OS/Arch counts

const htmlReport = `
<html>
  <head>
    <meta charset="utf-8" />
    <title>Top test failures</title>
    <style>
body {
  font-family: sans-serif;
  color: #222;
}
a {
  text-decoration: none;
}
table {
  border-spacing: 0;
  border-collapse: collapse;
}
table#failures {
  width: 100%;
  max-width: 100%;
}
table>caption {
  padding-top: 8px;
  padding-bottom: 8px;
  color: #777;
  text-align: left;
}
table>tbody>tr>td, table>tbody>tr>th, table>thead>tr>th {
  padding: 8px;
  vertical-align: top;
  line-height: 1.4;
}
table.lined>tbody>tr:not(.expand)>td, table.lined>tbody>tr:not(.expand)>th {
  border-top: 1px solid #ddd;
}
table.lined>thead>tr>th {
  vertical-align: bottom;
  border-bottom: 2px solid #ddd;
  border-top: 0px;
}
th {
  text-align: left;
}
table#failures>tbody>tr:not(.expand) {
  cursor: pointer;
}
table#failures>tbody>tr:not(.expand):hover {
  color: #337ab7;
}
td.pct, th.pct {
  text-align: right;
}
td.plus {
  color: #337ab7;
}
tr.expand {
  display: none;
}
table>tbody>tr.expand>td {
  padding-top: 0px;
}
a.hash {
  font-family: monospace;
  font-size: 120%;
}
.toggleRow {
  display: none;
}
    </style>
    <script src="https://ajax.googleapis.com/ajax/libs/jquery/1.8.2/jquery.min.js"></script>
  </head>
  <body>
    <table id="failures" class="lined">
      <caption>Test failures as of {{(lastRev .).Date.Format "02 Jan 15:04 2006"}}, sorted by chance the failure is still happening. Click row for details and culprits.</caption>
      <thead>
        <tr><th></th><th class="pct">P(current)</th><th class="pct">P(failure)</th><th style="width:100%">Failure</th></tr>
      </thead>
      {{range $i, $class := .}}
      {{$failuresByT := groupByT .Failures}}
      <tr><td class="plus">+</td><td class="pct">{{pct .Current}}</td><td class="pct">{{pct .Latest.FailureProbability}}</td><td>{{.Class.String}}</td></tr>
      <tr class="expand"><td></td><td colspan="3">
        <table>
          <tr><th>Chance failure is still happening</th><td>{{pct .Current}}</td></tr>
          {{with .Latest}}
          <tr><th>Failure probability</th><td>{{pct .FailureProbability}} ({{.Failures}} of {{numCommits .}} commits)</td></tr>
          {{if eq (numCommits .) 1}}
          <tr><th>Observed</th><td>{{template "observation" (index $failuresByT .First)}}</td></tr>
          {{else}}
          <tr><th>First observed</th><td>{{template "observation" (index $failuresByT .First)}}</td></tr>
          {{if ge (numCommits .) 2}}
          <tr><th></th><td><a href="#" class="toggleRows">show other observations</a></td></tr>
          {{range $_, $t := (slice .Times 1 -1)}}
          <tr class="toggleRow"><th></th><td>{{template "observation" (index $failuresByT $t)}}</td></tr>
          {{end}}
          {{end}}
          <tr><th>Last observed</th><td>{{template "observation" (index $failuresByT .Last)}}</td></tr>
          <tr><th>Likely culprits</th>
	    <td style="padding:0px">
	      <table>
		{{range (.Culprits 0.9 10)}}
		<tr><td class="pct">{{pct .P}}</td><td>{{template "revSubject" (index $class.Revs .T)}}</td></tr>
		{{end}}
	      </table>
	    </td>
          </tr>
          {{end}}{{/* numCommits == 1*/}}
          {{end}}{{/* with .Latest */}}
          {{with (slice .Test.All 1 (len .Test.All))}}
            <tr><th>{{len .}} past failure(s)</th><td><a href="#" class="toggleRows">show</a></td></tr>
            {{range .}}
              <tr class="toggleRow"><th></th><td>{{template "observation" (index $failuresByT .First)}} to {{template "observation" (index $failuresByT .Last)}}; {{pct .FailureProbability}} failure probability</td></tr>
            {{end}}
          {{else}}
            <tr><th>No known past failures</th></tr>
          {{end}}
        </table>
      </td></tr>
      {{end}}
    </table>
    <script>
$("#failures").click(function(ev) {
    var target = $(ev.target);
    if (target.closest("table").filter("#failures").length === 0)
      return;

    ev.stopPropagation();
    var tr = target.closest("tr");

    if (!tr.hasClass("expand")) {
        tr.next().toggle();
    }
});
$("a.toggleRows").click(function(ev) {
    ev.stopPropagation();
    $(ev.target).closest("tr").nextUntil(":not(.toggleRow)").toggle();
    var text = $(ev.target).text();
    text = text.replace(/show|hide/, function(x) { return x === "show" ? "hide" : "show"; });
    $(ev.target).text(text);
    return false;
});
    </script>
  </body>
</html>

{{/* observation expands a []*failure in to an observation line. */}}
{{define "observation"}}
{{$first := (index . 0)}}
{{template "revDate" $first.Rev}} ({{$first.CommitsAgo}} commits ago) on{{range .}} <a href="{{.Build.LogURL}}">{{.Build.Builder}}</a>{{end}}
{{end}}
{{/* revLink expands a *Revision to a link to that commit. */}}
{{define "revLink"}}
<a href="https://github.com/golang/go/commit/{{.Revision}}" class="hash">{{printf "%.7s" .Revision}}</a>
{{end}}
{{/* revDate expands a *Revision to the commit's hash and date. */}}
{{define "revDate"}}
{{template "revLink" .}} {{.Date.Format "02 Jan 15:04 2006"}}
{{end}}
{{/* revSubject expands a *Revision to the commit's hash and subject. */}}
{{define "revSubject"}}
{{template "revLink" .}} {{.Subject}}
{{end}}
`

var htmlFuncs = template.FuncMap(map[string]interface{}{
	"pct": pct,
	"lastRev": func(classes []*failureClass) *Revision {
		// TODO: Ugh. It's lame that the same Revs is in every
		// failureClass.
		revs := classes[0].Revs
		return revs[len(revs)-1]
	},
	"numCommits": func(r FlakeRegion) int {
		return r.Last - r.First + 1
	},
	"groupByT": func(failures []*failure) map[int][]*failure {
		out := make(map[int][]*failure)
		if len(failures) == 0 {
			return out
		}
		lastI, lastT := 0, failures[0].T
		for i := 1; i < len(failures); i++ {
			if failures[i].T != lastT {
				out[lastT] = failures[lastI:i]
				lastI, lastT = i, failures[i].T
			}
		}
		out[lastT] = failures[lastI:]
		return out
	},
	"slice": func(v interface{}, start, end int) interface{} {
		val := reflect.ValueOf(v)
		if start < 0 {
			start = val.Len() + start
		}
		if end < 0 {
			end = val.Len() + end
		}
		return val.Slice(start, end).Interface()
	},
})

var htmlTemplate = template.Must(template.New("report").Funcs(htmlFuncs).Parse(htmlReport))

func printHTMLReport(w io.Writer, classes []*failureClass) {
	err := htmlTemplate.Execute(w, classes)
	if err != nil {
		log.Fatal(err)
	}
}
