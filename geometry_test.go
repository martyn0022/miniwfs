package main

import (
	"math"
	"reflect"
	"testing"

	"github.com/golang/geo/r2"
	"github.com/golang/geo/s2"
)

func TestEncodeBbox(t *testing.T) {
	bbox, _ := parseBbox("8.5,47.9,8.9,49.2")
	got := EncodeBbox(bbox)
	expected := []float64{8.5, 47.9, 8.9, 49.2}
	if !reflect.DeepEqual(expected, got) {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestEncodeBbox_Empty(t *testing.T) {
	got := EncodeBbox(s2.EmptyRect())
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestGetTileBounds(t *testing.T) {
	b := EncodeBbox(getTileBounds(12, 2148, 1436))
	expectBbox("8.7890625,47.2195681,8.8769531,47.2792290", b, t)
}

func TestProjectWebMercator(t *testing.T) {
	// https://developers.google.com/maps/documentation/javascript/examples/map-coordinates
	got := projectWebMercator(s2.LatLngFromDegrees(41.850, -87.650))
	expected := r2.Point{X: 65.67111111111113, Y: 95.17492654697409}
	delta := got.Sub(expected)
	if math.Abs(delta.X)+math.Abs(delta.Y) > 1e-9 {
		t.Errorf("expected %v, got %v", expected, got)
	}
}

func expectBbox(expected string, got []float64, t *testing.T) {
	e, err := parseBbox(expected)
	if err != nil {
		t.Error(err)
		return
	}

	encoded := EncodeBbox(e)
	for i, f := range encoded {
		if math.Abs(f-got[i]) > 1e-7 {
			t.Errorf("expected %s, got %v", expected, got)
			return
		}
	}
}
