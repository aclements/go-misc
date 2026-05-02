package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

type NormalizeFlag uint32

const (
	// NormResultKeepLanes preserves the literal lane count in method return types.
	NormResultKeepLanes NormalizeFlag = 1 << iota
	// NormResultKeepLanesExact preserves the exact lane count in method return types without scaling.
	NormResultKeepLanesExact
	// NormResultKeepBase preserves the literal base type name in method return types.
	NormResultKeepBase
	// NormResultKeepBits preserves the literal bit width in method return types.
	NormResultKeepBits
	// NormNameKeepBase preserves the literal base type name inside method names.
	NormNameKeepBase
	// NormNameKeepLanes preserves the literal lane count inside method names.
	NormNameKeepLanes
	// NormNameKeepBits preserves the literal bit width inside method names.
	NormNameKeepBits
	// NormParamKeepUint8 preserves the literal 'uint8' basic type in the first parameter group.
	NormParamKeepUint8
	// NormParamKeepUint64 preserves the literal 'uint64' basic type in the first parameter group.
	NormParamKeepUint64
	// NormKeepAll bypasses all normalization entirely.
	NormKeepAll
	// NormForceMaskLanes coerces literal 'uint8' parameters into the '<b><M>' lane token, even if M < 8.
	NormForceMaskLanes
)

type NormalizedMethod struct {
	// Method is the original raw method metadata.
	Method *MethodMeta
	// NormName is the canonicalized operation name (e.g., "Abs", "ConvertToFloat32").
	NormName string
	// NormSig is the canonicalized signature string (e.g., "() T", "() Float32x<M>").
	NormSig string
	// IsRoundedUp is true if the result lane count was promoted to hit the 128-bit bound.
	IsRoundedUp bool
	// ReceiverBase is the extracted receiver group (e.g., "Int32", "Mask8").
	ReceiverBase string
	// Width is the bit width of the receiver type (128, 256, or 512).
	Width int
}

func normalizeMethods(typeGroups []*TypeGroup, fset *token.FileSet) []NormalizedMethod {
	var result []NormalizedMethod

	for _, tg := range typeGroups {
		switch tg.Type.Kind {
		case TypeVec, TypeMask:
		default:
			panic("bad type group kind")
		}
		receiverBase := fmt.Sprintf("%s%d", tg.Type.Base, tg.Type.Bits)
		width := tg.Type.Bits * tg.Type.Lanes

		for _, m := range tg.Funcs {
			flags := m.ComputeFlags()
			normName, rounded1 := normalizeName(m, flags)

			tBits := m.Receiver.Bits
			tLanes := m.Receiver.Lanes
			elemType := m.Receiver.getElemType()
			maskType := fmt.Sprintf("Mask%dx%d", tBits, tLanes)

			ft, rounded2 := normalizeAST(m, flags, elemType, maskType, fset)
			normSig := nodeToString(fset, ft)

			result = append(result, NormalizedMethod{
				Method:       m,
				NormName:     normName,
				NormSig:      normSig,
				IsRoundedUp:  rounded1 || rounded2,
				ReceiverBase: receiverBase,
				Width:        width,
			})
		}
	}

	return result
}

// normalizeAST clones a function's AST and normalizes its parameter and result types
// based on the provided NormalizeFlag rules. It returns the modified AST and a boolean
// indicating whether any lane counts were rounded up due to hardware constraints.
func normalizeAST(meta *MethodMeta, flags NormalizeFlag, elemType, maskType string, fset *token.FileSet) (*ast.FuncType, bool) {
	if meta.Decl == nil {
		return nil, false
	}
	if flags&NormKeepAll != 0 {
		return meta.Decl.Type, false
	}

	ft := cloneFuncType(fset, meta.Decl.Type)
	tLanes := meta.Receiver.Lanes
	typeName := meta.Receiver.Name
	roundedUpAny := false

	ast.Inspect(ft, func(n ast.Node) bool {
		if arr, ok := n.(*ast.ArrayType); ok && arr.Len != nil {
			if lit, ok := arr.Len.(*ast.BasicLit); ok && lit.Kind == token.INT {
				val, _ := strconv.Atoi(lit.Value)
				if val == tLanes {
					arr.Len = &ast.Ident{Name: "<M>"}
				}
			}
		}
		return true
	})

	normalizeFieldList := func(list *ast.FieldList, isResult bool) {
		if list == nil {
			return
		}
		for i, field := range list.List {
			isFirstGroup := i == 0

			isIndices := false
			for _, name := range field.Names {
				if name.Name == "indices" {
					isIndices = true
					break
				}
			}

			ast.Inspect(field.Type, func(n ast.Node) bool {
				if ident, ok := n.(*ast.Ident); ok {
					if flags&NormForceMaskLanes != 0 && ident.Name == "uint8" && !isResult {
						ident.Name = "<b><M>"
					} else if ident.Name == "uint8" && flags&NormParamKeepUint8 != 0 && !isResult && isFirstGroup {
						// Retain literal
					} else if ident.Name == "uint64" && flags&NormParamKeepUint64 != 0 && !isResult && isFirstGroup {
						// Retain literal
					} else if ident.Name == typeName && !(isResult && (flags&NormResultKeepLanes != 0)) && !isIndices {
						ident.Name = "T"
					} else if ident.Name == elemType {
						ident.Name = "<b><N>"
					} else if ident.Name == maskType {
						ident.Name = "Mask"
					} else {
						res, rounded := normalizeTypeName(ident.Name, meta, flags, isResult, false, !isResult && isIndices)
						ident.Name = res
						if rounded {
							roundedUpAny = true
						}
					}
				}
				return true
			})
		}
	}

	normalizeFieldList(ft.Params, false)
	normalizeFieldList(ft.Results, true)

	return ft, roundedUpAny
}

// normalizeName canonicalizes the provided MethodMeta name into a normalized form,
// enabling method aggregation across identical logical vector operations.
func normalizeName(meta *MethodMeta, flags NormalizeFlag) (string, bool) {
	if flags&NormKeepAll != 0 || strings.HasPrefix(meta.Name, "As") {
		return meta.Name, false
	}

	typeName := meta.Receiver.Name
	roundedUpAny := false

	if strings.Contains(meta.Name, typeName) {
		return strings.ReplaceAll(meta.Name, typeName, "<T>"), false
	}

	typeRegexRaw := regexp.MustCompile(`(Int|Uint|Float|Mask)(8|16|32|64)(x(2|4|8|16|32|64))?`)

	name := typeRegexRaw.ReplaceAllStringFunc(meta.Name, func(m string) string {
		res, rounded := normalizeTypeName(m, meta, flags, false, true, false)
		if rounded {
			roundedUpAny = true
		}
		return res
	})
	return name, roundedUpAny
}

// normalizeTypeName parses a single Go type name and transforms it based on the
// requested flags, substituting base types, bit widths, and lane counts with canonical tokens.
func normalizeTypeName(name string, meta *MethodMeta, flags NormalizeFlag, isResult, isName, skipBaseOverride bool) (string, bool) {
	skipLane := (isResult && (flags&NormResultKeepLanes != 0)) || (isName && (flags&NormNameKeepLanes != 0))
	skipBase := (isResult && (flags&NormResultKeepBase != 0)) || (isName && (flags&NormNameKeepBase != 0)) || skipBaseOverride
	skipBits := (isResult && (flags&NormResultKeepBits != 0)) || (isName && (flags&NormNameKeepBits != 0))

	tBase, tBits, tLanes := meta.Receiver.Base, meta.Receiver.Bits, meta.Receiver.Lanes
	roundedUp := false

	typeRegexRaw := regexp.MustCompile(`(Int|Uint|Float|Mask)(8|16|32|64)(x(2|4|8|16|32|64))?`)

	if m := typeRegexRaw.FindStringSubmatch(name); len(m) > 1 {
		base, bitsStr := m[1], m[2]
		bits, _ := strconv.Atoi(bitsStr)

		var sb strings.Builder
		if base == tBase && !skipBase {
			sb.WriteString("<B>")
		} else {
			sb.WriteString(base)
		}

		if skipBits {
			sb.WriteString(bitsStr)
		} else {
			if bits == tBits {
				sb.WriteString("<N>")
			} else if bits == tBits/2 {
				sb.WriteString("<N/2>")
			} else if bits == tBits*2 {
				sb.WriteString("<N*2>")
			} else {
				sb.WriteString(bitsStr)
			}
		}

		if len(m) > 3 && m[3] != "" {
			lanesStr := m[4]
			sb.WriteString("x")
			if skipLane {
				sb.WriteString(lanesStr)
			} else {
				lanes, _ := strconv.Atoi(lanesStr)

				expectedLanes := tLanes
				keepLanesExact := (isResult && (flags&NormResultKeepLanesExact != 0))

				if !keepLanesExact {
					if lanesStr == strconv.Itoa(tLanes/2) {
						expectedLanes = tLanes / 2
					} else if lanesStr == strconv.Itoa(tLanes/4) {
						expectedLanes = tLanes / 4
					} else if lanesStr == strconv.Itoa(tLanes*2) {
						expectedLanes = tLanes * 2
					}
				}

				effectiveExpectedLanes := expectedLanes
				if expectedLanes*bits < 128 {
					effectiveExpectedLanes = 128 / bits
					if lanes == effectiveExpectedLanes && lanes != expectedLanes {
						roundedUp = true
					}
				}

				if lanes == effectiveExpectedLanes {
					if keepLanesExact || expectedLanes == tLanes {
						sb.WriteString("<M>")
					} else if expectedLanes == tLanes/2 {
						sb.WriteString("<M/2>")
					} else if expectedLanes == tLanes/4 {
						sb.WriteString("<M/4>")
					} else if expectedLanes == tLanes*2 {
						sb.WriteString("<M*2>")
					}
				} else {
					sb.WriteString(lanesStr)
				}
			}
		}
		return sb.String(), roundedUp
	}

	if m := pureMaskRegex.FindStringSubmatch(name); len(m) > 1 {
		bitsStr, lanesStr := m[1], m[2]
		bits, _ := strconv.Atoi(bitsStr)

		var sb strings.Builder
		if tBase == "Mask" && !skipBase {
			sb.WriteString("<B>")
		} else {
			sb.WriteString("Mask")
		}

		if skipBits {
			sb.WriteString(bitsStr)
		} else {
			if bits == tBits {
				sb.WriteString("<N>")
			} else {
				sb.WriteString(bitsStr)
			}
		}

		sb.WriteString("x")
		if skipLane {
			sb.WriteString(lanesStr)
		} else {
			lanes, _ := strconv.Atoi(lanesStr)
			if lanes == tLanes {
				sb.WriteString("<M>")
			} else {
				sb.WriteString(lanesStr)
			}
		}
		return sb.String(), false
	}

	if m := basicRegex.FindStringSubmatch(name); len(m) > 1 {
		_, bitsStr := m[1], m[2]
		bits, _ := strconv.Atoi(bitsStr)

		if tBase == "Mask" {
			if bits == tLanes && !skipLane {
				return "<b><M>", false
			}
		} else {
			if bits == tBits {
				return "<b><N>", false
			}
			if bits == tBits/2 {
				return "<b><N/2>", false
			}
			if bits == tBits*2 {
				return "<b><N*2>", false
			}
		}
		return name, false
	}

	return name, false
}
