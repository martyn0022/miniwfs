package main

import (
	"encoding/json"
	//"fmt"
	"html"
	"io/ioutil"
	"log"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/golang/geo/s2"
	"github.com/paulmach/go.geojson"
)

type Index struct {
	Collections map[string]*Collection
	mutex       sync.RWMutex
	PublicPath  *url.URL
	watcher     *fsnotify.Watcher
}

type Collection struct {
	Features geojson.FeatureCollection
	bbox     []s2.Rect
	Path     string
	byID     map[string]int // "W77" -> 3 if Features[3].ID == "W77"
}

func MakeIndex(collections map[string]string, publicPath *url.URL) (*Index, error) {
	index := &Index{
		Collections: make(map[string]*Collection),
		PublicPath:  publicPath,
	}

	if watcher, err := fsnotify.NewWatcher(); err == nil {
		index.watcher = watcher
	} else {
		return nil, err
	}

	go index.watchFiles()
	for name, path := range collections {
		coll, err := readCollection(path)
		if err != nil {
			return nil, err
		}
		index.Collections[name] = coll
	}

	for _, c := range index.Collections {
		if err := index.watcher.Add(c.Path); err != nil {
			return nil, err
		}
	}

	return index, nil
}

func (index *Index) GetCollections() []string {
	index.mutex.RLock()
	defer index.mutex.RUnlock()

	result := make([]string, 0, len(index.Collections))
	for name, _ := range index.Collections {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (index *Index) GetItem(collection string, id string) *geojson.Feature {
	index.mutex.RLock()
	defer index.mutex.RUnlock()

	coll := index.Collections[collection]
	if coll == nil {
		return nil
	}

	if index, ok := coll.byID[id]; ok {
		return coll.Features.Features[index]
	} else {
		return nil
	}
}

func (index *Index) GetItems(collection string, limit int, bbox s2.Rect) *WFSFeatureCollection {
	index.mutex.RLock()
	defer index.mutex.RUnlock()

	if limit < 1 {
		limit = 1
	} else if limit > 10000 {
		limit = 10000
	}

	coll := index.Collections[collection]
	if coll == nil {
		return nil
	}

	// If we had more data, we could compute s2 cell coverages and only
	// check the intersection for features inside the coverage area.
	// But we operate on a few thousand features, so let's keep things simple
	// for the time being.
	result := &WFSFeatureCollection{Type: "FeatureCollection"}
	result.Features = make([]*geojson.Feature, 0, 50)
	bounds := s2.EmptyRect()
	var nextID string
	for i, featureBounds := range coll.bbox {
		if !bbox.Intersects(featureBounds) {
			continue
		}

		feature := coll.Features.Features[i]
		if len(result.Features) >= limit {
			nextID = getIDString(feature.ID)
			break
		}
		result.Features = append(result.Features, feature)
		bounds = bounds.Union(featureBounds)
	}

	if len(nextID) > 0 {
		// TODO: Return nextID as part of result.
		nextLink := &WFSLink{
			Rel:   "next",
			Title: "next",
			Type:  "application/geo+json",
		}
		nextLink.Href = index.PublicPath.String() + "collections/" + html.EscapeString(collection) + "/items?start=" + html.EscapeString(nextID) + "&limit=" + strconv.Itoa(limit)
		// TODO: Omit limit if it has default value
		// TODO: also add bbox param, unless it is entire world
		result.Links = append(result.Links, nextLink)
	}

	result.BoundingBox = encodeBbox(bounds)
	return result
}

func encodeBbox(r s2.Rect) []float64 {
	if r.IsEmpty() {
		return nil
	} else {
		bbox := [4]float64{
			r.Lo().Lng.Degrees(),
			r.Lo().Lat.Degrees(),
			r.Hi().Lng.Degrees(),
			r.Hi().Lat.Degrees(),
		}
		return bbox[0:4]
	}
}

func (index *Index) watchFiles() {
	for {
		select {
		case event, ok := <-index.watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write {
				if coll, err := readCollection(event.Name); err == nil {
					index.replaceCollection(coll)
				} else {
					log.Printf("error reading collection %s: %v",
						event.Name, err)
				}
			}
		}
	}
}

func (index *Index) replaceCollection(c *Collection) {
	index.mutex.Lock()
	defer index.mutex.Unlock()

	for name, old := range index.Collections {
		if c.Path == old.Path {
			index.Collections[name] = c
		}
	}
}

func readCollection(path string) (*Collection, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	data, err := ioutil.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	coll := &Collection{Path: absPath}
	if err := json.Unmarshal(data, &coll.Features); err != nil {
		return nil, err
	}

	bbox := make([]s2.Rect, len(coll.Features.Features))
	coll.bbox = bbox
	for i, f := range coll.Features.Features {
		if f != nil {
			bbox[i] = computeBounds(f.Geometry)
		}
	}

	byID := make(map[string]int)
	coll.byID = byID

	for i, f := range coll.Features.Features {
		id := getIDString(f.ID)
		if len(id) == 0 {
			id = getIDString(f.Properties["id"])
		}
		if len(id) == 0 {
			id = getIDString(f.Properties[".id"])
		}
		if len(id) > 0 {
			f.ID = id
			byID[id] = i
		}
	}

	return coll, nil
}

func getIDString(s interface{}) string {
	if str, ok := s.(string); ok {
		return str
	} else if i, ok := s.(int64); ok {
		return strconv.FormatInt(i, 10)
	} else {
		return ""
	}
}

func computeBounds(g *geojson.Geometry) s2.Rect {
	r := s2.EmptyRect()
	if g == nil {
		return r
	}

	switch g.Type {
	case geojson.GeometryPoint:
		if len(g.Point) >= 2 {
			r = r.AddPoint(s2.LatLngFromDegrees(g.Point[1], g.Point[0]))
		}
		return r

	case geojson.GeometryMultiPoint:
		for _, p := range g.MultiPoint {
			if len(p) >= 2 {
				r = r.AddPoint(s2.LatLngFromDegrees(p[1], p[0]))
			}
		}
		return r

	case geojson.GeometryLineString:
		return computeLineBounds(g.LineString)

	case geojson.GeometryMultiLineString:
		for _, line := range g.MultiLineString {
			r = r.Union(computeLineBounds(line))
		}
		return r

	case geojson.GeometryPolygon:
		for _, ring := range g.Polygon {
			r = r.Union(computeLineBounds(ring))
		}
		s2.ExpandForSubregions(r)
		return r

	case geojson.GeometryMultiPolygon:
		for _, poly := range g.MultiPolygon {
			for _, ring := range poly {
				r = r.Union(computeLineBounds(ring))
			}
			s2.ExpandForSubregions(r)
		}
		return r

	case geojson.GeometryCollection:
		for _, geometry := range g.Geometries {
			r = r.Union(computeBounds(geometry))
		}
		return r

	default:
		return r
	}
}

func computeLineBounds(line [][]float64) s2.Rect {
	r := s2.EmptyRect()
	for _, p := range line {
		if len(p) >= 2 {
			r = r.AddPoint(s2.LatLngFromDegrees(p[1], p[0]))
		}
	}
	return r
}
