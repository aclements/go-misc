package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"

	"golang.org/x/image/draw"
)

func main() {
	var err error

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s input output\n", os.Args[0])
	}
	flag.Parse()
	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}

	// Read input file.
	f, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		log.Fatal(err)
	}

	// Scale down by a factor of 2.
	sb := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, sb.Dx()/2, sb.Dy()/2))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, sb, draw.Over, nil)

	// Write output file.
	if f, err = os.Create(os.Args[2]); err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, dst); err != nil {
		log.Fatal(err)
	}
}
