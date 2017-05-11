// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gg

import (
	"fmt"
	"image/color"
	"math"
	"reflect"
	"strings"
	"time"

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

	// Ticks returns a set of "nice" major and minor tick marks
	// spanning this Scaler's domain. The returned tick locations
	// are values in this Scaler's domain type in increasing
	// order. labels[i] gives the label of the major tick at
	// major[i]. The minor ticks are a superset of the major
	// ticks.
	//
	// max and pred constrain the ticks returned by Ticks. If
	// possible, Ticks returns the largest set of ticks such that
	// there are no more than max major ticks and the ticks
	// satisfy pred. Both are hints, since for some scale types
	// there's no clear way to reduce the number of ticks.
	//
	// pred should return true if the given set of ticks is
	// acceptable. pred must be "monotonic" in the following
	// sense: if pred is true for a given set of ticks, it must be
	// true for any subset of those ticks and if pred is false for
	// a given set of ticks, it must be false for any superset of
	// those ticks. In other words, pred should return false if
	// there are "too many" ticks or they are "too close
	// together". If pred is nil, it is assumed to always be
	// satisfied.
	//
	// If no tick marks can be produced (for example, there are no
	// values in this Scaler's domain or the predicate cannot be
	// satisfied), Ticks returns nil, nil, nil.
	//
	// TODO: Should this return ticks in the input space, the
	// intermediate space, or the output space? moremath returns
	// values in the input space. Input space values doesn't work
	// for discrete scales if I want the ticks between values.
	// Intermediate space works for continuous and discrete
	// inputs, but not for discrete ranges (maybe that's okay) and
	// it's awkward for a caller to do anything with an
	// intermediate space value. Output space doesn't work with
	// this API because I change the plot location in the course
	// of layout without recomputing ticks. However, output space
	// could work if Scaler exposed tick levels, since I could
	// save the computed tick level across a re-layout and
	// recompute the output space ticks from that.
	Ticks(max int, pred func(major, minor table.Slice, labels []string) bool) (major, minor table.Slice, labels []string)

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
	// Currently we can't express 2.

	// SetMin and SetMax set the minimum and maximum values of
	// this Scalar's domain and return the Scalar. If v is nil, it
	// unsets the bound.
	//
	// v must be convertible to the Scaler's domain type. For
	// example, if this is a linear scale, v can be of any
	// numerical type. Unlike ExpandDomain, these do not set the
	// Scaler's domain type.
	SetMin(v interface{}) ContinuousScaler
	SetMax(v interface{}) ContinuousScaler

	// TODO: Should Include work on any Scaler?

	// Include requires that v be included in this Scaler's
	// domain. Like SetMin/SetMax, this can expand Scaler's
	// domain, but unlike SetMin/SetMax, this does not restrict
	// it. If v is nil, it does nothing.
	//
	// v must be convertible to the Scaler's domain type. Unlike
	// ExpandDomain, this does not set the Scaler's domain type.
	Include(v interface{}) ContinuousScaler
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

func (s *defaultScale) Ticks(max int, pred func(major, minor table.Slice, labels []string) bool) (major, minor table.Slice, labels []string) {
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

	case []time.Time:
		return NewTimeScaler(), nil
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

func (s *identityScale) Ticks(max int, pred func(major, minor table.Slice, labels []string) bool) (major, minor table.Slice, labels []string) {
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
	return &moremathScale{
		min:     math.NaN(),
		max:     math.NaN(),
		dataMin: math.NaN(),
		dataMax: math.NaN(),
	}
}

func NewLogScaler(base int) ContinuousScaler {
	return &moremathScale{
		min:     math.NaN(),
		max:     math.NaN(),
		base:    base,
		dataMin: math.NaN(),
		dataMax: math.NaN(),
	}
}

type moremathScale struct {
	r Ranger
	f interface{}

	domainType       reflect.Type
	base             int
	min, max         float64
	dataMin, dataMax float64
}

func (s *moremathScale) String() string {
	if s.base > 0 {
		return fmt.Sprintf("log [%d,%g,%g] => %s", s.base, s.min, s.max, s.r)
	}
	return fmt.Sprintf("linear [%g,%g] => %s", s.min, s.max, s.r)
}

func (s *moremathScale) ExpandDomain(vs table.Slice) {
	if s.domainType == nil {
		s.domainType = reflect.TypeOf(vs).Elem()
	}

	var data []float64
	slice.Convert(&data, vs)
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

func (s *moremathScale) SetMin(v interface{}) ContinuousScaler {
	if v == nil {
		s.min = math.NaN()
		return s
	}
	vfloat := reflect.ValueOf(v).Convert(float64Type).Float()
	s.min = vfloat
	return s
}

func (s *moremathScale) SetMax(v interface{}) ContinuousScaler {
	if v == nil {
		s.max = math.NaN()
		return s
	}
	vfloat := reflect.ValueOf(v).Convert(float64Type).Float()
	s.max = vfloat
	return s
}

func (s *moremathScale) Include(v interface{}) ContinuousScaler {
	if v == nil {
		return s
	}
	vfloat := reflect.ValueOf(v).Convert(float64Type).Float()
	if math.IsNaN(vfloat) || math.IsInf(vfloat, 0) {
		return s
	}
	if math.IsNaN(s.dataMin) {
		s.dataMin, s.dataMax = vfloat, vfloat
	} else {
		s.dataMin = math.Min(s.dataMin, vfloat)
		s.dataMax = math.Max(s.dataMax, vfloat)
	}
	return s
}

type tickMapper interface {
	scale.Ticker
	Map(float64) float64
}

func (s *moremathScale) get() tickMapper {
	min, max := s.min, s.max
	if min > max {
		min, max = max, min
	}
	if math.IsNaN(min) {
		min = s.dataMin
	}
	if math.IsNaN(max) {
		max = s.dataMax
	}
	if math.IsNaN(min) {
		// Only possible if both dataMin and dataMax are NaN.
		min, max = -1, 1
	}
	if s.base > 0 {
		ls, err := scale.NewLog(min, max, s.base)
		if err != nil {
			panic(err)
		}
		ls.SetClamp(true)
		return &ls
	}
	return &scale.Linear{
		Min: min, Max: max,
	}
}

func (s *moremathScale) Ranger(r Ranger) Ranger {
	old := s.r
	if r != nil {
		s.r = r
	}
	return old
}

func (s *moremathScale) RangeType() reflect.Type {
	return s.r.RangeType()
}

func (s *moremathScale) Map(x interface{}) interface{} {
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

func (s *moremathScale) Ticks(max int, pred func(major, minor table.Slice, labels []string) bool) (major, minor table.Slice, labels []string) {
	type Stringer interface {
		String() string
	}
	if s.domainType == nil {
		// There are no values and no domain type, so we can't
		// compute ticks or return slices of the domain type.
		return nil, nil, nil
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
	default:
		// Set bounds for the pred loop below.
		o.MinLevel, o.MaxLevel = -1000, 1000
	}
	ls := s.get()
	level, ok := o.FindLevel(ls, 0)
	if !ok {
		return nil, nil, nil
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
	// Adjust level to satisfy pred.
	for ; level <= o.MaxLevel; level++ {
		majorx := ls.TicksAtLevel(level)
		minorx := ls.TicksAtLevel(level - 1)
		labels := mkLabels(majorx.([]float64))

		// Convert to domain type.
		majorv := reflect.New(reflect.SliceOf(s.domainType))
		minorv := reflect.New(reflect.SliceOf(s.domainType))
		slice.Convert(majorv.Interface(), majorx)
		slice.Convert(minorv.Interface(), minorx)
		major, minor = majorv.Elem().Interface(), minorv.Elem().Interface()

		if pred == nil || pred(major, minor, labels) {
			return major, minor, labels
		}
	}
	Warning.Printf("%s: unable to compute satisfactory ticks, axis will be empty", s)
	return nil, nil, nil
}

func (s *moremathScale) SetFormatter(f interface{}) {
	s.f = f
}

func (s *moremathScale) CloneScaler() Scaler {
	s2 := *s
	return &s2
}

// NewTimeScaler returns a continuous linear scale. The domain must
// be time.Time.
func NewTimeScaler() *timeScale {
	return &timeScale{}
}

type timeScale struct {
	r                Ranger
	f                func(time.Time) string
	min, max         time.Time
	dataMin, dataMax time.Time
}

func (s *timeScale) String() string {
	return fmt.Sprintf("time [%g,%g] => %s", s.min, s.max, s.r)
}

func (s *timeScale) ExpandDomain(vs table.Slice) {
	var data []time.Time
	slice.Convert(&data, vs)
	min, max := s.dataMin, s.dataMax
	for _, v := range data {
		if v.Before(min) || min.IsZero() {
			min = v
		}
		if v.After(max) || max.IsZero() {
			max = v
		}
	}
	s.dataMin, s.dataMax = min, max
}

func (s *timeScale) SetMin(v interface{}) ContinuousScaler {
	s.min = v.(time.Time)
	return s
}

func (s *timeScale) SetMax(v interface{}) ContinuousScaler {
	s.max = v.(time.Time)
	return s
}

func (s *timeScale) Include(v interface{}) ContinuousScaler {
	tv := v.(time.Time)
	if s.dataMin.IsZero() {
		s.dataMin, s.dataMax = tv, tv
	} else {
		if tv.Before(s.dataMin) {
			s.dataMin = tv
		}
		if tv.After(s.dataMax) {
			s.dataMax = tv
		}
	}
	return s
}

func (s *timeScale) Ranger(r Ranger) Ranger {
	old := s.r
	if r != nil {
		s.r = r
	}
	return old
}

func (s *timeScale) RangeType() reflect.Type {
	return s.r.RangeType()
}

func (s *timeScale) getMinMax() (time.Time, time.Time) {
	min := s.min
	if min.IsZero() {
		min = s.dataMin
	}
	max := s.max
	if max.IsZero() {
		max = s.dataMax
	}
	return min, max
}

func (s *timeScale) Map(x interface{}) interface{} {
	min, max := s.getMinMax()
	t := x.(time.Time)
	var scaled float64 = float64(t.Sub(min)) / float64(max.Sub(min))

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

type durationTicks time.Duration

func (d durationTicks) Next(t time.Time) time.Time {
	if d == 0 {
		panic("invalid zero duration")
	}
	return t.Add(time.Duration(d)).Truncate(time.Duration(d))
}

var timeTickerLevels = []struct {
	min  time.Duration
	next func(t time.Time) time.Time
}{
	{time.Minute, durationTicks(time.Minute).Next},
	{10 * time.Minute, durationTicks(10 * time.Minute).Next},
	{time.Hour, func(t time.Time) time.Time {
		year, month, day := t.Date()
		// N.B. This will skip an hour at some DST transitions.
		return time.Date(year, month, day, t.Hour()+1, 0, 0, 0, t.Location())
	}},
	{6 * time.Hour, func(t time.Time) time.Time {
		year, month, day := t.Date()
		// N.B. This will skip an hour if the DST transition
		// happens at a multiple of 6 hours.
		return time.Date(year, month, day, ((t.Hour()+6)/6)*6, 0, 0, 0, t.Location())
	}},
	{24 * time.Hour, func(t time.Time) time.Time {
		year, month, day := t.Date()
		return time.Date(year, month, day+1, 0, 0, 0, 0, t.Location())
	}},
	{7 * 24 * time.Hour, func(t time.Time) time.Time {
		year, month, day := t.Date()
		loc := t.Location()
		_, week1 := t.ISOWeek()
		for {
			day++
			t = time.Date(year, month, day, 0, 0, 0, 0, loc)
			if _, week2 := t.ISOWeek(); week1 != week2 {
				return t
			}
		}
	}},
	{30 * 24 * time.Hour, func(t time.Time) time.Time {
		year, month, _ := t.Date()
		return time.Date(year, month+1, 1, 0, 0, 0, 0, t.Location())
	}},
	{365 * 24 * time.Hour, func(t time.Time) time.Time {
		return time.Date(t.Year()+1, time.January, 1, 0, 0, 0, 0, t.Location())
	}},
}

// timeTicker calculates the ticks between min and max. levels >= 0
// refer to entries in timeTickerLevels. levels < 0 start with -1 at
// every 10 seconds and then alternate dividing by 2 and 5. So level
// -3 is 1s, -9 is 1ms, -12 is 1us, etc.
// https://play.golang.org/p/xUv4P25Wxi will print the level step
// sizes.
type timeTicker struct {
	min, max time.Time
}

func (t *timeTicker) getNextTick(level int) func(time.Time) time.Time {
	if level >= 0 {
		if level >= len(timeTickerLevels) {
			// TODO: larger ticks should do multiples of
			// the year, like the linear scale does.
			panic(fmt.Sprintf("invalid level %d", level))
		}
		return timeTickerLevels[level].next
	} else {
		exp, double := level/2+1, (level%2 == 0)
		step := math.Pow10(exp) * 1e9
		if double {
			step = step * 5
		}
		return durationTicks(time.Duration(step)).Next
	}
}

func (t *timeTicker) CountTicks(level int) int {
	next := t.getNextTick(level)
	var i int
	// N.B. We cut off at 1e5 ticks. If your plot is larger than
	// that, you're on your own.
	for x := next(t.min.Add(-1)); !x.After(t.max) && i < 1e5; x = next(x) {
		i++
	}
	return i
}

func (t *timeTicker) TicksAtLevel(level int) interface{} {
	var ticks []time.Time
	next := t.getNextTick(level)
	for x := next(t.min.Add(-1)); !x.After(t.max); x = next(x) {
		ticks = append(ticks, x)
	}
	return ticks
}

func (t *timeTicker) GuessLevel() int {
	dur := t.max.Sub(t.min)
	for i := len(timeTickerLevels) - 1; i >= 0; i-- {
		if dur > timeTickerLevels[i].min {
			return i
		}
	}
	return int(2 * (math.Log10(float64(dur)/1e9) - 2))
}

func (timeTicker) MaxLevel() int {
	return len(timeTickerLevels) - 1
}

func (timeTicker) Label(cur, prev time.Time, level int) string {
	dateFmt := "2006"
	switch {
	case level < 6:
		dateFmt = "2006/1/2"
		if !prev.IsZero() {
			if prev.Year() == cur.Year() {
				dateFmt = "Jan 2"
				_, prevweek := prev.ISOWeek()
				_, curweek := cur.ISOWeek()
				if prevweek == curweek {
					dateFmt = "Mon"
					if prev.YearDay() == cur.YearDay() {
						dateFmt = ""
					}
				}
			}
		}
	case level < 7:
		dateFmt = "2006/1"
		if !prev.IsZero() && prev.Year() == cur.Year() {
			dateFmt = "Jan"
		}
	}
	timeFmt := ""
	switch {
	case level < -3: // < 1s
		digits := (-level - 2) / 2
		timeFmt = "15:04:05." + strings.Repeat("0", digits)
	case level < 0: // < 1m
		timeFmt = "15:04:05"
	case level < 4: // < 1d
		timeFmt = "15:04"
	}
	return cur.Format(strings.TrimSpace(dateFmt + " " + timeFmt))
}

func (s *timeScale) Ticks(maxTicks int, pred func(major, minor table.Slice, labels []string) bool) (table.Slice, table.Slice, []string) {
	min, max := s.getMinMax()
	ticker := &timeTicker{min, max}
	o := scale.TickOptions{Max: maxTicks, MinLevel: -21, MaxLevel: ticker.MaxLevel()}
	level, ok := o.FindLevel(ticker, ticker.GuessLevel())
	if !ok {
		// TODO(quentin): Better handling of too-large time range.
		return nil, nil, nil
	}
	mkLabels := func(major []time.Time) []string {
		// TODO(quentin): Pick a format based on which parts
		// of the time have changed and are non-zero.
		labels := make([]string, len(major))
		if s.f != nil {
			// Use custom formatter.
			for i, x := range major {
				labels[i] = s.f(x)
			}
			return labels
		}
		var prev time.Time
		for i, t := range major {
			labels[i] = ticker.Label(t, prev, level)
			prev = t
		}
		return labels
	}
	var majors, minors []time.Time
	var labels []string
	for ; level <= o.MaxLevel; level++ {
		majors = ticker.TicksAtLevel(level).([]time.Time)
		if level > o.MinLevel {
			minors = ticker.TicksAtLevel(level - 1).([]time.Time)
		}
		labels = mkLabels(majors)
		if pred == nil || pred(majors, minors, labels) {
			break
		}
	}
	return majors, minors, labels
}

func (s *timeScale) SetFormatter(f interface{}) {
	s.f = f.(func(time.Time) string)
}

func (s *timeScale) CloneScaler() Scaler {
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

func (s *ordinalScale) Ticks(max int, pred func(major, minor table.Slice, labels []string) bool) (major, minor table.Slice, labels []string) {
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
	if seq == nil {
		return reflect.MakeSlice(rt, 0, 0).Interface()
	}
	res := reflect.MakeSlice(rt, sv.Len(), sv.Len())
	for i, len := 0, sv.Len(); i < len; i++ {
		val := scaler.Map(sv.Index(i).Interface())
		res.Index(i).Set(reflect.ValueOf(val))
	}
	return res.Interface()
}
