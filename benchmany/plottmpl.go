// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

const plotHTML = `
<!DOCTYPE html>
<html>
    <head>
        <script type="text/javascript" src="https://www.google.com/jsapi"></script>
        <script type="text/javascript">
         google.load('visualization', '1.1', {packages: ['corechart']});
         google.setOnLoadCallback(drawChart);

         function drawChart() {
             // Parse the date column.
             tables.forEach(function(table) {
                 table.Cols.date = table.Cols.date.map(function(v) {
                     return new Date(v);
                 });
             });

             tables.forEach(function(table) {
                 createLine(document.body, "commits in {{.Revs}}", "normalized " + table.Unit, table);
             });
         }

         function tableToDataTable(cols, table) {
             var rows = new Array(table.Cols[cols[0].label].length);
             for (var rowi = 0; rowi < rows.length; rowi++)
                 rows[rowi] = {c: new Array(cols.length)};
             cols.forEach(function(col, coli) {
                 table.Cols[col.label].forEach(function(v, rowi) {
                     rows[rowi].c[coli] = {v: v};
                 });
             });
             return new google.visualization.DataTable({rows:rows,cols:cols});
         }

         function createLine(parent, xLabel, yLabel, table) {
             // Convert table into a DataTable.
             var cols = [{label: "i", type: "number"},
                         {label: "commit", type: "string", role: "tooltip"}];
             table.ColNames.forEach(function(name) {
                 if (name == "date" || name == "i" || name == "commit")
                     return;
                 cols.push({label: name, type: "number"});
             });
             var data = tableToDataTable(cols, table);

             var options = {
                 explorer: {keepInBounds: true, maxZoomOut: 1, maxZoomIn: 1/16},
                 chartArea: {left:'5%', width: '80%', height: '90%'},
                 hAxis: {title: xLabel, textPosition: "in"},
                 vAxis: {title: yLabel, textPosition: "in", minValue: 0},
                 focusTarget: "category",
             };
             var div = document.createElement("div");
             div.setAttribute("style", "width: 49vw; height: 49vh; display: inline-block");
             parent.appendChild(div);
             var chart = new google.visualization.LineChart(div);
             chart.draw(data, options);
         }
        </script>
    </head>
    <body>
        <p>
            {{.Title}}
        </p>
        <script type="text/javascript">
            var tables = {{.Tables}};
        </script>
    </body>
</html>
`
