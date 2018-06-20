// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

"use strict";

const ControlJump = 1
const ControlCall = 2
const ControlRet = 3
const ControlExit = 4

function disasm(container, insts) {
    const table = $('<table class="disasm">').appendTo($(container).empty());

    // Create a zero-height TD at the top that will contain the
    // control flow arrows SVG.
    const arrowTD = $("<td>");
    $("<tr>").appendTo(table).
        append($('<td colspan="3">')).
        append(arrowTD);
    var arrowSVG;

    // Create disassembly table.
    const rows = [];
    const pcToRow = new Map();
    for (var inst of insts) {
        const args = formatArgs(inst.Args);
        // Create the row. The last TD is to extend the highlight over
        // the arrows SVG.
        const row = $("<tr>").attr("tabindex", -1).
            append($("<td>").text("0x"+inst.PC.toString(16))).
            append($("<td>").text(inst.Op)).
            append($("<td>").append(args)).
            append($("<td>"));
        table.append(row);

        const rowMeta = {elt: row, i: rows.length, width: 1, arrows: []};
        rows.push(rowMeta);
        pcToRow.set(inst.PC, rowMeta);

        // Add a gap after strict block terminators.
        if (inst.Control.Type != 0) {
            if (inst.Control.Type != ControlCall && !inst.Control.Conditional)
                table.append($("<tr>").css({height: "1em"}));
        }

        // On-click handler.
        row.click(() => {
            row.focus();
            // Clear arrow highlights.
            $("path", arrowSVG).attr({stroke: "black"});
            // Highlight arrows.
            rowMeta.arrows.forEach((a) => a.attr({stroke: "red"}));
            // TODO: Change color of markers (annoyingly hard without SVG 2)
        });
    }

    // Collect control-flow arrows.
    const arrows = [];
    for (var inst of insts) {
        if (inst.Control.Type == 0)
            continue;

        arrows.push({0: pcToRow.get(inst.PC),
                     1: pcToRow.get(inst.Control.TargetPC),
                     pos: 0, control: inst.Control});
    }

    // Sort arrows by length.
    function alen(arrow) {
        const r1 = arrow[0], r2 = arrow[1];
        if (!r2)
            return 0;
        return Math.abs(r1.i - r2.i);
    }
    arrows.sort(function(a, b) {
        return alen(a) - alen(b);
    })

    // Place the arrows.
    var cols = 1;
    for (var arrow of arrows) {
        const r1 = arrow[0], r2 = arrow[1];
        if (!r2)
            continue;

        var pos = 0;
        for (var i = Math.min(r1.i, r2.i); i <= Math.max(r1.i, r2.i); i++)
            pos = Math.max(pos, rows[i].width);
        arrow.pos = pos;

        for (var i = Math.min(r1.i, r2.i); i <= Math.max(r1.i, r2.i); i++)
            rows[i].width = pos + 1;
        cols = Math.max(cols, pos + 1);
    }

    // Draw arrows.
    const arrowWidth = 16;
    const indent = 8;
    const markerHeight = 8;
    if (arrows.length > 0) {
        const rowHeight = rows[0].elt.height();
        const svgWidth = cols * arrowWidth;
        arrowTD.css({"vertical-align": "top", "width": svgWidth});
        const tdTop = arrowTD.offset().top;
        // Create the arrows SVG. This is absolutely positioned so the
        // row highlight in the other TRs can extend over it and has
        // pointer-events: none so hover passes through to the TR.
        arrowSVG = $(document.createElementNS("http://www.w3.org/2000/svg", "svg")).
            attr({height: table.height(), width: svgWidth}).
            css({position: "absolute", "pointer-events": "none"}).
            appendTo(arrowTD);
        for (var arrow of arrows) {
            const line = $(document.createElementNS("http://www.w3.org/2000/svg", "path"));
            line.appendTo(arrowSVG);

            const r1 = arrow[0], r2 = arrow[1];
            const y1 = r1.elt.offset().top - tdTop + rowHeight / 2;
            var marker = "url(#tri)";
            if (r2) {
                // In-function arrow.
                const x = arrow.pos * arrowWidth;;
                const y2 = r2.elt.offset().top - tdTop + rowHeight / 2;
                line.attr("d", "M" + x + " " + y1 +
                          " h" + indent +
                          " V" + y2 +
                          " h" + (-indent + markerHeight / 2));
            } else if (arrow.control.Type == ControlRet) {
                // Exit arrow.
                const y = r1.elt.offset().top - tdTop + rowHeight / 2;
                const w = arrowWidth - markerHeight;
                line.attr("d", "M " + (w + markerHeight) + " " + y + "h" + (-w));
            } else if (arrow.control.Type == ControlExit || arrow.control.TargetPC != 0) {
                // Out arrow.
                // TODO: Some other arrow for dynamic target.
                const y = r1.elt.offset().top - tdTop + rowHeight / 2;
                line.attr("d", "M 0 " + y + "h" + (arrowWidth - markerHeight));
                if (arrow.control.Type == ControlExit)
                    marker = "url(#markX)";
            }
            line.attr({stroke: "black", "stroke-width": "2px",
                       fill: "none", "marker-end": marker});

            // Attach the arrow to the outgoing and incoming
            // instructions.
            r1.arrows.push(line);
            if (r2)
                r2.arrows.push(line);
        }
    }
}

function formatArgs(args) {
    console.log("formatArgs",args);
    const elts = [];
    var i = 0;
    for (var arg of args) {
        if (i++ > 0)
            elts.push(document.createTextNode(", "));

        var r;
        if (r = /(.*)\(SB\)/.exec(arg)) {
            elts.push($("<a>").attr("href", "/s/" + r[1]).text(arg)[0]);
        } else {
            elts.push(document.createTextNode(arg))
        }
    }
    return $(elts);
}
