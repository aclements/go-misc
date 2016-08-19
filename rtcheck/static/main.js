"use strict";

function initOrder(strings, edges) {
    // Hook into the graph edges.
    var labelRe = /^l([0-9]+)-l([0-9]+)$/;
    $.each(edges, function(_, edge) {
        var g = $(document.getElementById(edge.EdgeID));
        // Increase the size of the click target by making a second,
        // invisible, larger path element.
        var path = $("path:first", g);
        path.clone().attr("stroke-width", "10px").attr("stroke", "transparent").appendTo(path.parent());
        // On click, update the info box.
        g.
          css({cursor: "pointer"}).
          on("click", function(ev) {
              showEdge(strings, edge);
          });
    });
    enableHighlighting($("#graph")[0]);
    zoomify($("#graph")[0], $("#graphWrap")[0]);
    $("#graph").css("visibility", "visible");
}

function showEdge(strings, edge) {
    var info = $("#info");
    info.empty().scrollTop(0);

    // Show summary information.
    $("<p>").appendTo(info).text(
        edge.Paths.length + " path(s) acquire " + edge.Locks[0] + ", then " + edge.Locks[1] + ":"
    ).css({fontWeight: "bold"});

    $.each(edge.Paths, function(_, path) {
        var p = $("<p>").appendTo(info).css("white-space", "nowrap");
        $("<div>").appendTo(p).text(strings[path.RootFn]);
        function posText(pathID, line) {
            // Keep only the trailing part of the path.
            return strings[pathID].replace(/.*\//, "") + ":" + line;
        }
        function renderStack(stack) {
            var elided = [];
            var elideDiv;
            // Render each frame.
            $.each(stack.Op, function(i) {
                var div = $("<div>").appendTo(p);
                var indent = i == 0 ? "1em" : "2em";
                var showFirst = 2, showLast = 3;
                if (i >= showFirst && stack.Op.length - i > showLast) {
                    // Elide middle of the path.
                    if (elided.length === 0) {
                        elideDiv = $("<div>").appendTo(p).css("padding-left", indent);
                    }
                    elided.push(div[0]);
                }
                div.appendTo(p);
                // TODO: Link to path somehow.
                div.text(strings[stack.Op[i]] + " at " + posText(stack.P[i], stack.L[i]));
                div.css("padding-left", indent);
            });
            // If we elided frames, update the show link.
            if (elided.length === 1) {
                // No point in eliding one frame.
                elideDiv.hide();
            } else if (elided.length > 0) {
                elideDiv.text("... show " + elided.length + " elided frames ...").css({color: "#00e", cursor: "pointer"});
                $(elided).hide();
                elideDiv.on("click", function(ev) {
                    elideDiv.hide();
                    $(elided).show();
                })
            }
        }
        renderStack(path.From);
        renderStack(path.To);
    });
}

// enableHighlighting takes an GraphViz-generated SVG and enables
// interactive highlighting when the mouse hovers over nodes and
// edges.
function enableHighlighting(svg) {
    var nodes = {}, edges = {};
    function all(opacity) {
        $.each(nodes, function(_, node) {
            $(node.dom).clearQueue().fadeTo('fast', opacity);
        })
        $.each(edges, function(_, edge) {
            $(edge.dom).clearQueue().fadeTo('fast', opacity);
        })
    }

    function highlight(dom) {
        $(dom).clearQueue().fadeTo('fast', 1);
    }

    // Process nodes.
    $(".node", svg).each(function(_, node) {
        var id = $("title", node).text();
        var info = {dom: node, edges: []};
        nodes[id] = info;
        $(node).on("mouseenter", function() {
            all(0.25);
            highlight(node);
            $.each(info.edges, function(_, edge) {
                highlight(edge.dom);
                if (edge.from !== id)
                    highlight(nodes[edge.from].dom);
                if (edge.to !== id)
                    highlight(nodes[edge.to].dom);
            });
        }).on("mouseleave", function() {
            all(1);
        });
    });

    // Process edges.
    $(".edge", svg).each(function(_, edge) {
        var id = $("title", edge).text();
        var m = id.match(/^(.*)->(.*)$/);
        var info = {dom: edge, from: m[1], to: m[2]};
        edges[edge.id] = info;
        nodes[info.from].edges.push(info);
        nodes[info.to].edges.push(info);
        $(edge).on("mouseenter", function() {
            all(0.25);
            highlight(edge);
            highlight(nodes[info.from].dom);
            highlight(nodes[info.to].dom);
        }).on("mouseleave", function() {
            all(1);
        });
    });
}

// zoomify makes drags and wheel events on element fill pan and zoom
// svg. It initially centers the svg at (0, 0) and scales it down to
// fit in fill. Hence, the caller should center the svg element within
// fill.
function zoomify(svg, fill) {
    var svg = $(svg);
    var fill = $(fill);

    // Wrap svg in a group we can transform.
    var g = $(document.createElementNS("http://www.w3.org/2000/svg", "g"));
    g.append(svg.children()).appendTo(svg);

    // Create an initial transform to center and fit the svg.
    var bbox = g[0].getBBox();
    var scale = Math.min(fill.width() / bbox.width, fill.height() / bbox.height);
    if (scale > 1) scale = 1;
    var mat = svg[0].createSVGMatrix().
        translate(-bbox.x, -bbox.y).
        scale(scale).
        translate(-bbox.width/2, -bbox.height/2);
    var transform = svg[0].createSVGTransform();
    transform.setMatrix(mat);
    g[0].transform.baseVal.insertItemBefore(transform, 0);

    // Handle drags.
    var lastpos;
    function mousemove(ev) {
        if (ev.buttons == 0) {
            fill.off("mousemove");
            return;
        }
        var deltaX = ev.pageX - lastpos.pageX;
        var deltaY = ev.pageY - lastpos.pageY;
        lastpos = ev;
        var transform = svg[0].createSVGTransform();
        transform.setTranslate(deltaX, deltaY);
        g[0].transform.baseVal.insertItemBefore(transform, 0);
        g[0].transform.baseVal.consolidate();
        ev.preventDefault();
    }
    fill.on("mousedown", function(ev) {
        lastpos = ev;
        fill.on("mousemove", mousemove);
        ev.preventDefault();
    });
    fill.on("mouseup", function(ev) {
        fill.off("mousemove");
        ev.preventDefault();
    });

    // Handle zooms.
    var point = svg[0].createSVGPoint();
    fill.on("wheel", function(ev) {
        var delta = ev.originalEvent.deltaY;
        // rates is the delta required to scale by a factor of 2.
        var rates = [
            500, // WheelEvent.DOM_DELTA_PIXEL
            30,  // WheelEvent.DOM_DELTA_LINE
            0.5, // WheelEvent.DOM_DELTA_PAGE
        ];
        var factor = Math.pow(2, -delta / rates[ev.originalEvent.deltaMode]);
        point.x = ev.clientX === undefined ? ev.originalEvent.clientX : ev.clientX;
        point.y = ev.clientY === undefined ? ev.originalEvent.clientY : ev.clientY;
        var center = point.matrixTransform(svg[0].getScreenCTM().inverse());

        // Scale by factor around center.
        var mat = svg[0].createSVGMatrix().
                         translate(center.x, center.y).
                         scale(factor).
                         translate(-center.x, -center.y);
        var transform = svg[0].createSVGTransform();
        transform.setMatrix(mat);
        g[0].transform.baseVal.insertItemBefore(transform, 0);
        g[0].transform.baseVal.consolidate();
        ev.preventDefault();
    });
}
