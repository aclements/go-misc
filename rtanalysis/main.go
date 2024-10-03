package main

import (
	"github.com/aclements/go-misc/rtanalysis/systemstack"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(systemstack.Analyzer) }
