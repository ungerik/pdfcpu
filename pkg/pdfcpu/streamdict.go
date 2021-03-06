/*
Copyright 2018 The pdfcpu Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pdfcpu

import (
	"fmt"

	"github.com/hhrutter/pdfcpu/pkg/filter"
	"github.com/hhrutter/pdfcpu/pkg/log"
	"github.com/pkg/errors"
)

// PDFFilter represents a PDF stream filter object.
type PDFFilter struct {
	Name        string
	DecodeParms *Dict
}

// StreamDict represents a PDF stream dict object.
type StreamDict struct {
	Dict
	StreamOffset      int64
	StreamLength      *int64
	StreamLengthObjNr *int
	FilterPipeline    []PDFFilter
	Raw               []byte // Encoded
	Content           []byte // Decoded
	IsPageContent     bool
}

// NewStreamDict creates a new PDFStreamDict for given PDFDict, stream offset and length.
func NewStreamDict(dict Dict, streamOffset int64, streamLength *int64, streamLengthObjNr *int,
	filterPipeline []PDFFilter) StreamDict {
	return StreamDict{dict, streamOffset, streamLength, streamLengthObjNr, filterPipeline, nil, nil, false}
}

// HasSoleFilterNamed returns true if there is exactly one filter defined for a stream dict.
func (streamDict StreamDict) HasSoleFilterNamed(filterName string) bool {

	fpl := streamDict.FilterPipeline
	if fpl == nil {
		return false
	}

	if len(fpl) != 1 {
		return false
	}

	soleFilter := fpl[0]

	return soleFilter.Name == filterName
}

// ObjectStreamDict represents a object stream dictionary.
type ObjectStreamDict struct {
	StreamDict
	Prolog         []byte
	ObjCount       int
	FirstObjOffset int
	ObjArray       Array
}

// NewObjectStreamDict creates a new ObjectStreamDict object.
func NewObjectStreamDict() *ObjectStreamDict {

	streamDict := StreamDict{Dict: NewDict()}

	streamDict.Insert("Type", Name("ObjStm"))
	streamDict.Insert("Filter", Name(filter.Flate))

	streamDict.FilterPipeline = []PDFFilter{{Name: filter.Flate, DecodeParms: nil}}

	return &ObjectStreamDict{StreamDict: streamDict}
}

// IndexedObject returns the object at given index from a ObjectStreamDict.
func (oStreamDict *ObjectStreamDict) IndexedObject(index int) (Object, error) {
	if oStreamDict.ObjArray == nil {
		return nil, errors.Errorf("IndexedObject(%d): object not available", index)
	}
	return oStreamDict.ObjArray[index], nil
}

// AddObject adds another object to this object stream.
// Relies on decoded content!
func (oStreamDict *ObjectStreamDict) AddObject(objNumber int, entry *XRefTableEntry) error {

	offset := len(oStreamDict.Content)

	s := ""
	if oStreamDict.ObjCount > 0 {
		s = " "
	}
	s = s + fmt.Sprintf("%d %d", objNumber, offset)

	oStreamDict.Prolog = append(oStreamDict.Prolog, []byte(s)...)

	var pdfString string

	// TODO Use fallthrough ?
	switch obj := entry.Object.(type) {

	case Dict:
		pdfString = obj.PDFString()

	case Array:
		pdfString = obj.PDFString()

	case Integer:
		pdfString = obj.PDFString()

	case Float:
		pdfString = obj.PDFString()

	case StringLiteral:
		pdfString = obj.PDFString()

	case HexLiteral:
		pdfString = obj.PDFString()

	case Boolean:
		pdfString = obj.PDFString()

	case Name:
		pdfString = obj.PDFString()

	default:
		return errors.Errorf("AddObject: undefined PDF object #%d\n", objNumber)

	}

	oStreamDict.Content = append(oStreamDict.Content, []byte(pdfString)...)
	oStreamDict.ObjCount++

	log.Debug.Printf("AddObject end : ObjCount:%d prolog = <%s> Content = <%s>\n", oStreamDict.ObjCount, oStreamDict.Prolog, oStreamDict.Content)

	return nil
}

// Finalize prepares the final content of the objectstream.
func (oStreamDict *ObjectStreamDict) Finalize() {
	oStreamDict.Content = append(oStreamDict.Prolog, oStreamDict.Content...)
	oStreamDict.FirstObjOffset = len(oStreamDict.Prolog)
	log.Debug.Printf("Finalize : firstObjOffset:%d Content = <%s>\n", oStreamDict.FirstObjOffset, oStreamDict.Content)
}

// XRefStreamDict represents a cross reference stream dictionary.
type XRefStreamDict struct {
	StreamDict
	Size           int
	Objects        []int
	W              [3]int
	PreviousOffset *int64
}

// NewXRefStreamDict creates a new PDFXRefStreamDict object.
func NewXRefStreamDict(ctx *Context) *XRefStreamDict {

	streamDict := StreamDict{Dict: NewDict()}

	streamDict.Insert("Type", Name("XRef"))
	streamDict.Insert("Filter", Name(filter.Flate))
	streamDict.FilterPipeline = []PDFFilter{{Name: filter.Flate, DecodeParms: nil}}

	streamDict.Insert("Root", *ctx.Root)

	if ctx.Info != nil {
		streamDict.Insert("Info", *ctx.Info)
	}

	if ctx.ID != nil {
		streamDict.Insert("ID", *ctx.ID)
	}

	if ctx.Encrypt != nil && ctx.EncKey != nil {
		streamDict.Insert("Encrypt", *ctx.Encrypt)
	}

	return &XRefStreamDict{StreamDict: streamDict}
}
