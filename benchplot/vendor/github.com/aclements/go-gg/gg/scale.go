// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gg

import (
	"fmt"
	"image/color"
	"math"
	"reflect"

	"github.com/aclements/go-gg/generic"
	"github.com/aclements/go-gg/generic/slice"
	"github.com/aclements/go-gg/palette"
	"github.com/aclements/go-gg/table"
	"github.com/aclements/go-moremath/scale"
)

// Continuous -> Interpolatable? Definitely.
//
// Continuous -> Discrete? Can always discretize the input either in
// value order or in index order. In this case the transform (linear,
// log, etc) doesn't matter as long as it's order-preserving. OTOH, a
// continuous input scale can be asked to map *any* value of its input
// type, but if I do this I can only map values that were trained.
// That suggests that I have to just bin the range to do this mapping.
//
// Discrete -> Interpolatable? Pick evenly spaced values on [0,1].
//
// Discrete -> Discrete? Definitely. Cycle the range if it's not long
// enough. If the input range is a VarNominal, concatenate the
// sequences and use index ordering.
//
// It's not really "continuous", it's more specifically cardinal.

// TODO: time.Time and time.Duration scalers. For time.Duration,
// handle sub-second as (10 seconds)^n (for n <= 0), handle seconds to
// minutes at multiples of 10 seconds, and likewise minutes to hours
// as multiples of 10 minutes, and handle hours as (10 hours)^n.

// XXX
//
// A Scaler can be cardinal, discrete, or identity.
//
// A cardinal Scaler has a VarCardinal input domain. If its output
// range is continuous, it maps an interval over the input to an
// interval of the output (possibly through a transformation such as a
// logarithm). If its output range is discrete, the input is
// discretized in value order and it acts like a discrete scale.
//
// XXX The cardinal -> discrete rule means we need to keep all of the
// input data, rather than just its bounds, just in case the range is
// discrete. Maybe it should just be a bucketing rule?
//
// A discrete Scaler has a VarNominal input domain. If the input is
// VarOrdinal, its order is used; otherwise, index order is imposed.
// If the output range is continuous, a discrete Scaler maps its input
// to the centers of equal sub-intervals of [0, 1] and then applies
// the Ranger. If the output range is discrete, the Scaler maps the
// Nth input level to the N%len(range)th output value.
//
// An identity Scaler ignores its input domain and output range and
// uses an identity function for mapping input to output. This is
// useful for specifying aesthetics directly, such as color or size,
// and is especially useful for constant Vars.
//
// XXX Should identity Scalers map numeric types to float64? Maybe it
// should depend on the range type of the ranger?
//
// XXX Arrange documentation as X -> Y?
type Scaler interface {
	// XXX

	ExpandDomain(table.Slice)

	// Ranger sets this Scaler's output range if r is non-nil and
	// returns the previous range. If a scale's Ranger is nil, it
	// will be assigned a default Ranger based on its aesthetic
	// when the Plot is rendered.
	Ranger(r Ranger) Ranger

	// XXX Should RangeType be implied by the aesthetic?
	//
	// XXX Should this be a method of Ranger instead?
	RangeType() reflect.Type

	// XXX
	//
	// x must be of the same type as the values in the domain Var.
	//
	// XXX Or should this take a slice? Or even a Var? That would
	// also eliminate RangeType(), though then Map would need to
	// know how to make the right type of return slice. Unless we
	// pushed slice mapping all the way to Ranger.
	//
	// XXX We could eliminate ExpandDomain if the caller was
	// required to pass everything to this at once and this did
	// the scale training. That would also make it easy to
	// implement the cardinal -> discrete by value order rule.
	// This would probably also make Map much faster.
	//
	// XXX If x is Unscaled, Map must only apply the ranger.
	Map(x interface{}) interface{}

	// XXX What should this return? moremath returns values in the
	// input space, but that obviously doesn't work for discrete
	// scales if I want the ticks between values. It could return
	// values in the intermediate space or the output space.
	// Intermediate space works for continuous and discrete
	// inputs, but not for discrete ranges (maybe that's okay).
	// Output space is bad because I change the plot location in
	// the course of layout. Currently it returns values in the
	// input space or nil if ticks don't make sense.
	Ticks(max int, pred func(major []float64, labels []string) bool) (major, minor table.Slice, labels []string)

	// SetFormatter sets the formatter for values on this scale.
	//
	// f may be nil, which makes this Scaler use the default
	// formatting. Otherwise, f must be a func(T) string where T
	// is convertible from the Scaler's input type (note that this
	// is weaker than typical Go function calls, which require
	// that the argument be assignable; this makes it possible to
	// use general-purpose functions like func(float64) string
	// even for more specific input types).
	SetFormatter(f interface{})

	CloneScaler() Scaler
}

type ContinuousScaler interface {
	Scaler

	// TODO: There are two variations on min/max. 1) We can force
	// the min/max, even if there's data beyond it. 2) We can say
	// cap the scale to some min/max, but a smaller range is okay.

	SetMin(v float64) ContinuousScaler
	SetMax(v float64) ContinuousScaler

	// TODO: Should Include take an interface{} and work on any
	// Scalar?

	Include(v float64) ContinuousScaler
}

// Unscaled represents a value that should not be scaled, but instead
// mapped directly to the output range. For continuous scales, this
// should be a value between 0 and 1. For discrete scales, this should
// be an integral value.
//
// TODO: This is confusing for opacity and size because it *doesn't*
// specify an exact opacity or size ratio since their default rangers
// aren't [0,1]. Maybe Unscaled should bypass scaling altogether (and
// only work if the range type is float64).
type Unscaled float64

var float64Type = reflect.TypeOf(float64(0))
var colorType = reflect.TypeOf((*color.Color)(nil)).Elem()

var canCardinal = map[reflect.Kind]bool{
	reflect.Float32: true,
	reflect.Float64: true,
	reflect.Int:     true,
	reflect.Int8:    true,
	reflect.Int16:   true,
	reflect.Int32:   true,
	reflect.Int64:   true,
	reflect.Uint:    true,
	reflect.Uintptr: true,
	reflect.Uint8:   true,
	reflect.Uint16:  true,
	reflect.Uint32:  true,
	reflect.Uint64:  true,
}

func isCardinal(k reflect.Kind) bool {
	// XXX Move this to generic.IsCardinalR and rename CanOrderR
	// to IsOrderedR. Does complex count? It supports most
	// arithmetic operators. Maybe cardinal is a plot concept and
	// not a generic concept? If sort.Interface influences this,
	// this may need to be a question about a Slice, not a
	// reflect.Kind.
	return canCardinal[k]
}

type defaultScale struct {
	scale Scaler

	// Pre-instantiation state.
	r         Ranger
	formatter interface{}
}

func (s *defaultScale) String() string {
	return fmt.Sprintf("default (%s)", s.scale)
}

func (s *defaultScale) ExpandDomain(v table.Slice) {
	if s.scale == nil {
		var err error
		s.scale, err = DefaultScale(v)
		if err != nil {
			panic(&generic.TypeError{reflect.TypeOf(v), nil, err.Error()})
		}
		s.instantiate()
	}
	s.scale.ExpandDomain(v)
}

func (s *defaultScale) ensure() Scaler {
	if s.scale == nil {
		s.scale = NewLinearScaler()
		s.instantiate()
	}
	return s.scale
}

// instantiate applies the pre-instantiation state to the newly
// instantiated s.scale and clears the state in s.
func (s *defaultScale) instantiate() {
	if s.r != nil {
		s.scale.Ranger(s.r)
		s.r = nil
	}
	if s.formatter != nil {
		s.scale.SetFormatter(s.formatter)
		s.formatter = nil
	}
}

func (s *defaultScale) Ranger(r Ranger) Ranger {
	// If there's no underlying scale yet, record the Ranger
	// locally rather than trying to guess a scale. This way users
	// can easily set Rangers before training any data.
	if s.scale == nil {
		old := s.r
		s.r = r
		return old
	}
	return s.scale.Ranger(r)
}

func (s *defaultScale) RangeType() reflect.Type {
	if s.scale == nil {
		return s.r.RangeType()
	}
	return s.scale.RangeType()
}

func (s *defaultScale) Map(x interface{}) interface{} {
	return s.ensure().Map(x)
}

func (s *defaultScale) Ticks(max int, pred func(major []float64, labels []string) bool) (major, minor table.Slice, labels []string) {
	return s.ensure().Ticks(max, pred)
}

func (s *defaultScale) SetFormatter(f interface{}) {
	if s.scale == nil {
		s.formatter = f
		return
	}
	s.scale.SetFormatter(f)
}

func (s *defaultScale) CloneScaler() Scaler {
	if s.scale == nil {
		return &defaultScale{nil, s.r, s.formatter}
	}
	return &defaultScale{s.scale.CloneScaler(), nil, s.formatter}
}

func DefaultScale(seq table.Slice) (Scaler, error) {
	// Handle common case types.
	switch seq.(type) {
	case []float64, []int, []uint:
		return NewLinearScaler(), nil

	case []string:
		// TODO: Ordinal scale
	}

	rt := reflect.TypeOf(seq).Elem()
	rtk := rt.Kind()

	switch {
	case rt.Implements(colorType):
		// For things that are already visual values, use an
		// identity scale.
		return NewIdentityScale(), nil

		// TODO: GroupAuto needs to make similar
		// cardinal/ordinal/nominal decisions. Deduplicate
		// these better.
	case isCardinal(rtk):
		return NewLinearScaler(), nil

	case slice.CanSort(seq):
		return NewOrdinalScale(), nil

	case rt.Comparable():
		// TODO: Nominal scale
		panic("not implemented")
	}

	return nil, fmt.Errorf("no default scale type for %T", seq)
}

// defaultRanger returns the default Ranger for the given aesthetic.
// If aes is an axis aesthetic, it returns nil (since these Rangers
// are assigned at render time). If aes is unknown, it panics.
func defaultRanger(aes string) Ranger {
	switch aes {
	case "x", "y":
		return nil

	case "stroke", "fill":
		return &defaultColorRanger{}

	case "opacity":
		return NewFloatRanger(0.1, 1)

	case "size":
		// Default to ranging between 1% and 10% of the
		// minimum plot dimension.
		return NewFloatRanger(0.01, 0.1)
	}

	panic(fmt.Sprintf("unknown aesthetic %q", aes))
}

// TODO: I'd like to remove identity scales and expose only Unscaled,
// but I use identity scales for physical types like color.Color right
// now. Probably that should bypass Scaler altogether.

func NewIdentityScale() Scaler {
	return &identityScale{}
}

type identityScale struct {
	rangeType reflect.Type
}

func (s *identityScale) ExpandDomain(v table.Slice) {
	s.rangeType = reflect.TypeOf(v).Elem()
}

func (s *identityScale) RangeType() reflect.Type {
	return s.rangeType
}

func (s *identityScale) Ranger(r Ranger) Ranger        { return nil }
func (s *identityScale) Map(x interface{}) interface{} { return x }

func (s *identityScale) Ticks(max int, pred func(major []float64, labels []string) bool) (major, minor table.Slice, labels []string) {
	return nil, nil, nil
}

func (s *identityScale) SetFormatter(f interface{}) {}

func (s *identityScale) CloneScaler() Scaler {
	s2 := *s
	return &s2
}

// NewLinearScaler returns a continuous linear scale. The domain must
// be a VarCardinal.
//
// XXX If I return a Scaler, I can't have methods for setting fixed
// bounds and such. I don't really want to expose the whole type.
// Maybe a sub-interface for continuous Scalers?
func NewLinearScaler() ContinuousScaler {
	// TODO: Control over base.
	return &linearScale{
		s:       scale.Linear{Min: math.NaN(), Max: math.NaN()},
		dataMin: math.NaN(),
		dataMax: math.NaN(),
	}
}

type linearScale struct {
	s scale.Linear
	r Ranger
	f interface{}

	domainType       reflect.Type
	dataMin, dataMax float64
}

func (s *linearScale) String() string {
	return fmt.Sprintf("linear [%g,%g] => %s", s.s.Min, s.s.Max, s.r)
}

func (s *linearScale) ExpandDomain(v table.Slice) {
	if s.domainType == nil {
		s.domainType = reflect.TypeOf(v).Elem()
	}

	var data []float64
	slice.Convert(&data, v)
	min, max := s.dataMin, s.dataMax
	for _, v := range data {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if v < min || math.IsNaN(min) {
			min = v
		}
		if v > max || math.IsNaN(max) {
			max = v
		}
	}
	s.dataMin, s.dataMax = min, max
}

func (s *linearScale) SetMin(v float64) ContinuousScaler {
	s.s.Min = v
	return s
}

func (s *linearScale) SetMax(v float64) ContinuousScaler {
	s.s.Max = v
	return s
}

func (s *linearScale) Include(v float64) ContinuousScaler {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return s
	}
	if math.IsNaN(s.dataMin) {
		s.dataMin, s.dataMax = v, v
	} else {
		s.dataMin = math.Min(s.dataMin, v)
		s.dataMax = math.Max(s.dataMax, v)
	}
	return s
}

func (s *linearScale) get() scale.Linear {
	ls := s.s
	if ls.Min > ls.Max {
		ls.Min, ls.Max = ls.Max, ls.Min
	}
	if math.IsNaN(ls.Min) {
		ls.Min = s.dataMin
	}
	if math.IsNaN(ls.Max) {
		ls.Max = s.dataMax
	}
	if math.IsNaN(ls.Min) {
		// Only possible if both dataMin and dataMax are NaN.
		ls.Min, ls.Max = -1, 1
	}
	return ls
}

func (s *linearScale) Ranger(r Ranger) Ranger {
	old := s.r
	if r != nil {
		s.r = r
	}
	return old
}

func (s *linearScale) RangeType() reflect.Type {
	return s.r.RangeType()
}

func (s *linearScale) Map(x interface{}) interface{} {
	ls := s.get()
	var scaled float64
	switch x := x.(type) {
	case float64:
		scaled = ls.Map(x)
	case Unscaled:
		scaled = float64(x)
	default:
		v := reflect.ValueOf(x).Convert(float64Type).Float()
		scaled = ls.Map(v)
	}

	switch r := s.r.(type) {
	case ContinuousRanger:
		return r.Map(scaled)

	case DiscreteRanger:
		_, levels := r.Levels()
		// Bin the scaled value into 'levels' bins.
		level := int(scaled * float64(levels))
		if level < 0 {
			level = 0
		} else if level >= levels {
			level = levels - 1
		}
		return r.MapLevel(level, levels)

	default:
		panic("Ranger must be a ContinuousRanger or DiscreteRanger")
	}
}

func (s *linearScale) Ticks(max int, pred func(major []float64, labels []string) bool) (major, minor table.Slice, labels []string) {
	type Stringer interface {
		String() string
	}
	o := scale.TickOptions{Max: max}

	// If the domain type is integral, don't let the tick level go
	// below 0. This is particularly important if the domain type
	// is a Stringer since the conversion back to the domain type
	// will cut off any fractional part.
	switch s.domainType.Kind() {
	case reflect.Int, reflect.Uint, reflect.Uintptr,
		reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		o.MinLevel, o.MaxLevel = 0, 1000
	}

	mkLabels := func(major []float64) []string {
		// Compute labels.
		labels = make([]string, len(major))
		if s.f != nil {
			// Use custom formatter.
			if f, ok := s.f.(func(float64) string); ok {
				// Fast path.
				for i, x := range major {
					labels[i] = f(x)
				}
				return labels
			}
			// TODO: Type check for better error.
			fv := reflect.ValueOf(s.f)
			at := fv.Type().In(0)
			var avs [1]reflect.Value
			for i, x := range major {
				avs[0] = reflect.ValueOf(x).Convert(at)
				rvs := fv.Call(avs[:])
				labels[i] = rvs[0].Interface().(string)
			}
			return labels
		}
		if s.domainType != nil {
			z := reflect.Zero(s.domainType).Interface()
			if _, ok := z.(Stringer); ok {
				// Convert the ticks back into the domain type
				// and use its String method.
				for i, x := range major {
					v := reflect.ValueOf(x).Convert(s.domainType)
					labels[i] = v.Interface().(Stringer).String()
				}
				return labels
			}
		}
		// Otherwise, just format them as floats.
		for i, x := range major {
			labels[i] = fmt.Sprintf("%.6g", x)
		}
		return labels
	}
	if pred != nil {
		o.Pred = func(ticks []float64, level int) bool {
			return pred(ticks, mkLabels(ticks))
		}
	}

	ls := s.get()
	majorx, minorx := ls.Ticks(o)
	return majorx, minorx, mkLabels(majorx)
}

func (s *linearScale) SetFormatter(f interface{}) {
	s.f = f
}

func (s *linearScale) CloneScaler() Scaler {
	s2 := *s
	return &s2
}

// TODO: The ordinal scale can only work with values it actually sees
// in the data. It has no sense of the type's actual domain. If the
// type is an enumerated type, we could fill in intermediate values
// and the caller could set a min and max for the scale to enumerate
// between.

func NewOrdinalScale() Scaler {
	return &ordinalScale{}
}

type ordinalScale struct {
	allData []slice.T
	r       Ranger
	f       interface{}
	ordered table.Slice
	index   map[interface{}]int
}

func (s *ordinalScale) ExpandDomain(v table.Slice) {
	// TODO: Type-check? For example, if I try to use a cardinal
	// type for "Color" and then a continuous type, this will
	// crash confusingly only once Map calls makeIndex and
	// NubAppend tries to make a consistently typed slice.
	s.allData = append(s.allData, slice.T(v))
	s.ordered, s.index = nil, nil
}

func (s *ordinalScale) Ranger(r Ranger) Ranger {
	old := s.r
	if r != nil {
		s.r = r
	}
	return old
}

func (s *ordinalScale) RangeType() reflect.Type {
	return s.r.RangeType()
}

func (s *ordinalScale) makeIndex() {
	if s.index != nil {
		return
	}

	// Compute ordered data index and cache.
	s.ordered = slice.NubAppend(s.allData...)
	slice.Sort(s.ordered)
	ov := reflect.ValueOf(s.ordered)
	s.index = make(map[interface{}]int, ov.Len())
	for i, len := 0, ov.Len(); i < len; i++ {
		s.index[ov.Index(i).Interface()] = i
	}
}

func (s *ordinalScale) Map(x interface{}) interface{} {
	var i int
	switch x := x.(type) {
	case Unscaled:
		i = int(x)
	default:
		s.makeIndex()
		i = s.index[x]
	}

	switch r := s.r.(type) {
	case DiscreteRanger:
		minLevels, maxLevels := r.Levels()
		if len(s.index) <= minLevels {
			return r.MapLevel(i, minLevels)
		} else if len(s.index) <= maxLevels {
			return r.MapLevel(i, len(s.index))
		} else {
			// TODO: Binning would also be a reasonable
			// policy.
			return r.MapLevel(i%maxLevels, maxLevels)
		}

	case ContinuousRanger:
		// Map i to the "middle" of the ith equal j-way
		// subdivision of [0, 1].
		j := len(s.index)
		x := (float64(i) + 0.5) / float64(j)
		return r.Map(x)

	default:
		panic("Ranger must be a ContinuousRanger or DiscreteRanger")
	}
}

func (s *ordinalScale) Ticks(max int, pred func(major []float64, labels []string) bool) (major, minor table.Slice, labels []string) {
	// TODO: Return *no* ticks and only labels. Can't currently
	// express this.

	// TODO: Honor constraints.

	s.makeIndex()
	labels = make([]string, len(s.index))
	ov := reflect.ValueOf(s.ordered)

	if s.f != nil {
		// Use custom formatter.
		// TODO: Type check for better error.
		fv := reflect.ValueOf(s.f)
		at := fv.Type().In(0)
		var avs [1]reflect.Value
		for i, len := 0, ov.Len(); i < len; i++ {
			avs[0] = ov.Index(i).Convert(at)
			rvs := fv.Call(avs[:])
			labels[i] = rvs[0].Interface().(string)
		}
	} else {
		// Use String() method or standard format.
		for i, len := 0, ov.Len(); i < len; i++ {
			labels[i] = fmt.Sprintf("%v", ov.Index(i).Interface())
		}
	}
	return s.ordered, nil, labels
}

func (s *ordinalScale) SetFormatter(f interface{}) {
	s.f = f
}

func (s *ordinalScale) CloneScaler() Scaler {
	ns := &ordinalScale{
		allData: make([]slice.T, len(s.allData)),
		r:       s.r,
	}
	for i, v := range s.allData {
		ns.allData[i] = v
	}
	return s
}

// XXX
//
// A Ranger must be either a ContinuousRanger or a DiscreteRanger.
type Ranger interface {
	RangeType() reflect.Type
}

type ContinuousRanger interface {
	Ranger
	Map(x float64) (y interface{})
	Unmap(y interface{}) (x float64, ok bool)
}

type DiscreteRanger interface {
	Ranger
	Levels() (min, max int)
	MapLevel(i, j int) interface{}
}

func NewFloatRanger(lo, hi float64) ContinuousRanger {
	return &floatRanger{lo, hi - lo}
}

type floatRanger struct {
	lo, w float64
}

func (r *floatRanger) String() string {
	return fmt.Sprintf("[%g,%g]", r.lo, r.lo+r.w)
}

func (r *floatRanger) RangeType() reflect.Type {
	return float64Type
}

func (r *floatRanger) Map(x float64) interface{} {
	return x*r.w + r.lo
}

func (r *floatRanger) Unmap(y interface{}) (float64, bool) {
	switch y := y.(type) {
	default:
		return 0, false

	case float64:
		return (y - r.lo) / r.w, true
	}
}

func NewColorRanger(palette []color.Color) DiscreteRanger {
	// TODO: Support continuous palettes.
	//
	// TODO: Support discrete palettes that vary depending on the
	// number of levels.
	return &colorRanger{palette}
}

type colorRanger struct {
	palette []color.Color
}

func (r *colorRanger) RangeType() reflect.Type {
	return colorType
}

func (r *colorRanger) Levels() (min, max int) {
	return len(r.palette), len(r.palette)
}

func (r *colorRanger) MapLevel(i, j int) interface{} {
	if i < 0 {
		i = 0
	} else if i >= len(r.palette) {
		i = len(r.palette) - 1
	}
	return r.palette[i]
}

// defaultColorRanger is the default color ranger. It is both a
// ContinuousRanger and a DiscreteRanger.
type defaultColorRanger struct{}

// autoPalette is the discrete palette used by defaultColorRanger.
var autoPalette = []color.Color{
	color.RGBA{0x4c, 0x72, 0xb0, 0xff},
	color.RGBA{0x55, 0xa8, 0x68, 0xff},
	color.RGBA{0xc4, 0x4e, 0x52, 0xff},
	color.RGBA{0x81, 0x72, 0xb2, 0xff},
	color.RGBA{0xcc, 0xb9, 0x74, 0xff},
	color.RGBA{0x64, 0xb5, 0xcd, 0xff},
}

func (r *defaultColorRanger) RangeType() reflect.Type {
	return colorType
}

func (r *defaultColorRanger) Map(x float64) interface{} {
	return palette.Viridis.Map(x)
}

func (r *defaultColorRanger) Unmap(y interface{}) (float64, bool) {
	switch y := y.(type) {
	default:
		return 0, false

	case color.RGBA:
		return float64(y.G) / float64(226), true
	}
}

func (r *defaultColorRanger) Levels() (min, max int) {
	return len(autoPalette), len(autoPalette)
}

func (r *defaultColorRanger) MapLevel(i, j int) interface{} {
	if i < 0 {
		i = 0
	} else if i >= len(autoPalette) {
		i = len(autoPalette) - 1
	}
	return autoPalette[i]
}

// mapMany applies scaler.Map to all of the values in seq and returns
// a slice of the results.
//
// TODO: Maybe this should just be how Scaler.Map works.
func mapMany(scaler Scaler, seq table.Slice) table.Slice {
	sv := reflect.ValueOf(seq)
	rt := reflect.SliceOf(scaler.RangeType())
	res := reflect.MakeSlice(rt, sv.Len(), sv.Len())
	for i, len := 0, sv.Len(); i < len; i++ {
		val := scaler.Map(sv.Index(i).Interface())
		res.Index(i).Set(reflect.ValueOf(val))
	}
	return res.Interface()
}
