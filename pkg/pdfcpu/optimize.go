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
	"sort"
	"strings"

	"github.com/hhrutter/pdfcpu/pkg/log"
	"github.com/pkg/errors"
)

// Mark all content streams for a page dictionary (for stats).
func identifyPageContent(xRefTable *XRefTable, pageDict *Dict, pageNumber, pageObjNumber int) error {

	log.Debug.Println("identifyPageContent begin")

	Object, found := pageDict.Find("Contents")
	if !found {
		log.Debug.Println("identifyPageContent end: no \"Contents\"")
		return nil
	}

	var contentArr Array

	if indRef, ok := Object.(IndirectRef); ok {

		entry, found := xRefTable.FindTableEntry(indRef.ObjectNumber.Value(), indRef.GenerationNumber.Value())
		if !found {
			return errors.Errorf("identifyPageContent: obj#:%d illegal indRef for Contents\n", pageObjNumber)
		}

		contentStreamDict, ok := entry.Object.(StreamDict)
		if ok {
			contentStreamDict.IsPageContent = true
			entry.Object = contentStreamDict
			log.Debug.Printf("identifyPageContent end: ok obj#%d\n", indRef.ObjectNumber.Value())
			return nil
		}

		contentArr, ok = entry.Object.(Array)
		if !ok {
			return errors.Errorf("identifyPageContent: obj#:%d page content entry neither stream dict nor array.\n", pageObjNumber)
		}

	} else if contentArr, ok = Object.(Array); !ok {
		return errors.Errorf("identifyPageContent: obj#:%d corrupt page content array\n", pageObjNumber)
	}

	for _, c := range contentArr {

		indRef, ok := c.(IndirectRef)
		if !ok {
			return errors.Errorf("identifyPageContent: obj#:%d corrupt page content array entry\n", pageObjNumber)
		}

		entry, found := xRefTable.FindTableEntry(indRef.ObjectNumber.Value(), indRef.GenerationNumber.Value())
		if !found {
			return errors.Errorf("identifyPageContent: obj#:%d illegal indRef for Contents\n", pageObjNumber)
		}

		contentStreamDict, ok := entry.Object.(StreamDict)
		if !ok {
			return errors.Errorf("identifyPageContent: obj#:%d page content entry is no stream dict\n", pageObjNumber)
		}

		contentStreamDict.IsPageContent = true
		entry.Object = contentStreamDict
		log.Debug.Printf("identifyPageContent: ok obj#%d\n", indRef.GenerationNumber.Value())
	}

	log.Debug.Println("identifyPageContent end")

	return nil
}

// ResourcesDictForPageDict returns the resource dict for a page dict if there is any.
func resourcesDictForPageDict(xRefTable *XRefTable, pageDict *Dict, pageObjNumber int) (*Dict, error) {

	obj, found := pageDict.Find("Resources")
	if !found {
		log.Debug.Printf("resourcesDictForPageDict end: No resources dict for page object %d, may be inheritated\n", pageObjNumber)
		return nil, nil
	}

	return xRefTable.DereferenceDict(obj)
}

func handleDuplicateFontObject(ctx *Context, font *Dict, fontName, resourceName string, objNr, pageNumber int) (*int, error) {

	fontObjectNumbers, found := ctx.Optimize.Fonts[fontName]
	if !found {
		return nil, nil
	}

	pageFonts := ctx.Optimize.PageFonts[pageNumber]

	for _, fontObjectNumber := range fontObjectNumbers {

		fontObject := ctx.Optimize.FontObjects[fontObjectNumber]

		log.Debug.Printf("handleDuplicateFontObject: comparing with fontDict Obj %d\n", fontObjectNumber)

		ok, err := equalFontDicts(fontObject.FontDict, font, ctx.XRefTable)
		if err != nil {
			return nil, err
		}

		if ok {

			// We have detected a redundant font dict.
			log.Debug.Printf("handleDuplicateFontObject: redundant fontObj#:%d basefont %s already registered with obj#:%d !\n", objNr, fontName, fontObjectNumber)
			// This is an optimization patch of the fontobject for a fontResource

			pageFonts[fontObjectNumber] = true

			fontObject.AddResourceName(resourceName)

			ctx.Optimize.DuplicateFonts[objNr] = font

			return &fontObjectNumber, nil
		}
	}

	return nil, nil
}

func pageFonts(ctx *Context, pageNumber int) (pageFonts IntSet) {

	pageFonts = ctx.Optimize.PageFonts[pageNumber]

	if pageFonts == nil {
		pageFonts = IntSet{}
		ctx.Optimize.PageFonts[pageNumber] = pageFonts
	}

	return
}

func fontName(ctx *Context, fontDict *Dict, objNumber int) (fontName string, err error) {

	var found bool
	var o Object

	if *fontDict.Subtype() != "Type3" {

		o, found = fontDict.Find("BaseFont")
		if !found {
			o, found = fontDict.Find("Name")
			if !found {
				return "", errors.New("fontName: missing fontDict entries \"BaseFont\" and \"Name\"")
			}
		}

	} else {

		// Type3 fonts only have Name in V1.0 else use generic name.

		o, found = fontDict.Find("Name")
		if !found {
			return fmt.Sprintf("Type3_%d", objNumber), nil
		}

	}

	o, err = ctx.Dereference(o)
	if err != nil {
		return "", err
	}

	baseFont, ok := o.(Name)
	if !ok {
		return "", errors.New("fontName: corrupt fontDict entry BaseFont")
	}

	return string(baseFont), nil
}

// Get rid of redundant fonts for given fontResources dictionary.
func optimizeFontResourcesDict(ctx *Context, fontResourcesDict *Dict, pageNumber, pageObjNumber int) error {

	log.Debug.Printf("optimizeFontResourcesDict begin: page=%d pageObjNumber=%d %s\nPageFonts=%v\n", pageNumber, pageObjNumber, *fontResourcesDict, ctx.Optimize.PageFonts)

	pageFonts := pageFonts(ctx, pageNumber)

	for resourceName, v := range *fontResourcesDict {

		indRef, ok := v.(IndirectRef)
		if !ok {
			return errors.Errorf("optimizeFontResourcesDict: missing indirect object ref for Font: %s\n", resourceName)
		}

		log.Debug.Printf("optimizeFontResourcesDict: processing font: %s, %s\n", resourceName, indRef)
		objectNumber := int(indRef.ObjectNumber)
		log.Debug.Printf("optimizeFontResourcesDict: objectNumber = %d\n", objectNumber)

		if _, found := ctx.Optimize.FontObjects[objectNumber]; found {
			//logInfoOptimizePrintf("optimizeFontResourcesDict: Fontobject %d already registered\n", objectNumber)
			pageFonts[objectNumber] = true
			continue
		}

		Object, err := ctx.Dereference(indRef)
		if err != nil {
			return errors.Errorf("optimizeFontResourcesDict: missing obj for indirect object ref %d:\n%s", objectNumber, err)
		}

		fontDict := Object.(Dict)
		log.Debug.Printf("optimizeFontResourcesDict: fontDict: %s\n", fontDict)

		if fontDict.Type() == nil {
			return errors.Errorf("optimizeFontResourcesDict: missing dict type %s\n", v)
		}

		if *fontDict.Type() != "Font" {
			return errors.Errorf("optimizeFontResourcesDict: expected Type=Font, unexpected Type: %s", *fontDict.Type())
		}

		var fn string
		fn, err = fontName(ctx, &fontDict, objectNumber)
		if err != nil {
			return err
		}

		log.Debug.Printf("optimizeFontResourcesDict: baseFont: %s\n", fn)

		// Isolate fontname prefix
		var prefix string
		i := strings.Index(fn, "+")

		if i > 0 {
			prefix = fn[:i]
			fn = fn[i+1:]
		}

		uniqueFontObjNr, err := handleDuplicateFontObject(ctx, &fontDict, fn, resourceName, indRef.ObjectNumber.Value(), pageNumber)
		if err != nil {
			return err
		}

		if uniqueFontObjNr == nil {

			// add fontInfo entry into Fonts
			// add fontobject entry into fontObjects
			log.Debug.Printf("optimizeFontResourcesDict: adding new font %s obj#%d\n", fn, objectNumber)

			fontObjectNumbers, found := ctx.Optimize.Fonts[fn]
			if found {
				log.Debug.Printf("optimizeFontResourcesDict: appending %d to %s\n", objectNumber, fn)
				ctx.Optimize.Fonts[fn] = append(fontObjectNumbers, objectNumber)
			} else {
				ctx.Optimize.Fonts[fn] = []int{objectNumber}
			}

			ctx.Optimize.FontObjects[objectNumber] =
				&FontObject{
					ResourceNames: []string{resourceName},
					Prefix:        prefix,
					FontName:      fn,
					FontDict:      &fontDict,
				}

			pageFonts[objectNumber] = true

		} else {
			// Update
			(*fontResourcesDict)[resourceName] = *NewIndirectRef(*uniqueFontObjNr, 0)
		}
	}

	log.Debug.Println("optimizeFontResourcesDict end:")

	return nil
}

func handleDuplicateImageObject(ctx *Context, image *StreamDict, resourceName string, objNr, pageNumber int) (*int, error) {

	pageImages := ctx.Optimize.PageImages[pageNumber]

	// Process image dict, check if this is a duplicate.
	for imageObjectNumber, imageObject := range ctx.Optimize.ImageObjects {

		log.Debug.Printf("handleDuplicateImageObject: comparing with imagedict Obj %d\n", imageObjectNumber)

		ok, err := equalStreamDicts(imageObject.ImageDict, image, ctx.XRefTable)
		if err != nil {
			return nil, err
		}

		if ok {

			// We have detected a redundant image dict.
			log.Debug.Printf("handleDuplicateImageObject: redundant imageObj#:%d already registered with obj#:%d !\n", objNr, imageObjectNumber)
			// This is an optimization patch of the imageobject for an XObject Resource:

			pageImages[imageObjectNumber] = true

			imageObject.AddResourceName(resourceName)

			ctx.Optimize.DuplicateImages[objNr] = image

			log.Debug.Printf("handleDuplicateImageObject: increment binary image duplsize for obj:%d: %d bytes\n", objNr, *image.StreamLength)

			return &imageObjectNumber, nil
		}
	}

	return nil, nil
}

// Get rid of redundant XObjects e.g. embedded images.
func optimizeXObjectResourcesDict(ctx *Context, xObjectResourcesDict *Dict, pageNumber, pageObjNumber int) error {

	log.Debug.Printf("optimizeXObjectResourcesDict begin: %s\n", *xObjectResourcesDict)

	pageImages := ctx.Optimize.PageImages[pageNumber]
	if pageImages == nil {
		pageImages = IntSet{}
		ctx.Optimize.PageImages[pageNumber] = pageImages
	}

	for resourceName, v := range *xObjectResourcesDict {

		indRef, ok := v.(IndirectRef)
		if !ok {
			return errors.Errorf("optimizeXObjectResourcesDict: missing indirect object ref for resourceId: %s", resourceName)
		}

		log.Debug.Printf("optimizeXObjectResourcesDict: processing xobject: %s, %s\n", resourceName, indRef)
		objectNumber := int(indRef.ObjectNumber)
		log.Debug.Printf("optimizeXObjectResourcesDict: objectNumber = %d\n", objectNumber)

		Object, err := ctx.Dereference(indRef)
		if err != nil {
			return errors.Errorf("optimizeXObjectResourcesDict: missing obj for indirect object ref %d:\n%s", objectNumber, err)
		}

		log.Debug.Printf("optimizeXObjectResourcesDict: dereferenced obj:%d\n%s", objectNumber, Object)

		xObjectStreamDict, ok := Object.(StreamDict)
		if !ok {
			return errors.Errorf("optimizeXObjectResourcesDict: unexpected Object: %s\n", v)
		}

		if xObjectStreamDict.Dict.Subtype() == nil {
			return errors.Errorf("optimizeXObjectResourcesDict: missing stream dict Subtype %s\n", v)
		}

		if *xObjectStreamDict.Dict.Subtype() == "Image" {

			// Already registered image object that appears in different resources dicts.
			if _, found := ctx.Optimize.ImageObjects[objectNumber]; found {
				log.Debug.Printf("optimizeXObjectResourcesDict: Imageobject %d already registered\n", objectNumber)
				pageImages[objectNumber] = true
				continue
			}

			uniqueImgObjNr, err := handleDuplicateImageObject(ctx, &xObjectStreamDict, resourceName, indRef.ObjectNumber.Value(), pageNumber)
			if err != nil {
				return err
			}

			if uniqueImgObjNr == nil {

				// Register new image dict.
				log.Debug.Printf("optimizeXObjectResourcesDict: adding new image obj#%d\n", objectNumber)

				ctx.Optimize.ImageObjects[objectNumber] =
					&ImageObject{
						ResourceNames: []string{resourceName},
						ImageDict:     &xObjectStreamDict,
					}

				pageImages[objectNumber] = true

				log.Debug.Printf("optimizeXObjectResourcesDict: increment binary image size for obj:%d: %d bytes\n", objectNumber, *xObjectStreamDict.StreamLength)

			} else {
				// Update
				(*xObjectResourcesDict)[resourceName] = *NewIndirectRef(*uniqueImgObjNr, 0)
			}

			continue
		}

		if *xObjectStreamDict.Subtype() != "Form" {
			log.Debug.Printf("optimizeXObjectResourcesDict: unexpected stream dict Subtype %s\n", *xObjectStreamDict.Dict.Subtype())
			continue
		}

		// Process form dict
		log.Debug.Printf("optimizeXObjectResourcesDict: parsing form dict obj:%d\n", objectNumber)
		parseResourcesDict(ctx, &xObjectStreamDict.Dict, pageNumber, objectNumber)
	}

	log.Debug.Println("optimizeXObjectResourcesDict end")

	return nil
}

// Optimize given resource dictionary by removing redundant fonts and images.
func optimizeResources(ctx *Context, resourcesDict *Dict, pageNumber, pageObjNumber int) error {

	log.Debug.Printf("optimizeResources begin: pageNumber=%d pageObjNumber=%d\n", pageNumber, pageObjNumber)

	if resourcesDict == nil {
		log.Debug.Printf("optimizeResources end: No resources dict available")
		return nil
	}

	// Process Font resource dict, get rid of redundant fonts.
	obj, found := resourcesDict.Find("Font")
	if found {

		dict, err := ctx.DereferenceDict(obj)
		if err != nil {
			return err
		}

		if dict == nil {
			return errors.Errorf("optimizeResources: font resource dict is null for page %d pageObj %d\n", pageNumber, pageObjNumber)
		}

		err = optimizeFontResourcesDict(ctx, dict, pageNumber, pageObjNumber)
		if err != nil {
			return err
		}

	}

	// Note: An optional ExtGState resource dict may contain binary content in the following entries: "SMask", "HT".

	// Process XObject resource dict, get rid of redundant images.
	obj, found = resourcesDict.Find("XObject")
	if found {

		dict, err := ctx.DereferenceDict(obj)
		if err != nil {
			return err
		}

		if dict == nil {
			return errors.Errorf("optimizeResources: xobject resource dict is null for page %d pageObj %d\n", pageNumber, pageObjNumber)
		}

		err = optimizeXObjectResourcesDict(ctx, dict, pageNumber, pageObjNumber)
		if err != nil {
			return err
		}

	}

	log.Debug.Println("optimizeResources end")

	return nil
}

// Process the resources dictionary for given page number and optimize by removing redundant resources.
func parseResourcesDict(ctx *Context, pageDict *Dict, pageNumber, pageObjNumber int) error {

	log.Debug.Printf("parseResourcesDict begin page: %d, object:%d\n", pageNumber+1, pageObjNumber)

	// Get resources dict for this page.
	dict, err := resourcesDictForPageDict(ctx.XRefTable, pageDict, pageObjNumber)
	if err != nil {
		return err
	}

	// dict may be nil for inheritated resource dicts.
	if dict != nil {

		// Optimize image and font resources.
		err = optimizeResources(ctx, dict, pageNumber, pageObjNumber)
		if err != nil {
			return err
		}

	}

	log.Debug.Printf("parseResourcesDict end page: %d, object:%d\n", pageNumber+1, pageObjNumber)

	return nil
}

// Iterate over all pages and optimize resources.
func parsePagesDict(ctx *Context, pagesDict *Dict, pageNumber int) (int, error) {

	log.Debug.Printf("parsePagesDict begin (next page=%d): %s\n", pageNumber+1, *pagesDict)

	// Get number of pages of this PDF file.
	count, found := pagesDict.Find("Count")
	if !found {
		return 0, errors.New("parsePagesDict: missing Count")
	}

	log.Debug.Printf("parsePagesDict: This page node has %d pages\n", int(count.(Integer)))

	// Iterate over page tree.
	kidsArray := pagesDict.ArrayEntry("Kids")
	for _, v := range *kidsArray {

		// Dereference next page node dict.
		indRef, _ := v.(IndirectRef)
		log.Debug.Printf("parsePagesDict PageNode: %s\n", indRef)
		Object, err := ctx.Dereference(indRef)
		if err != nil {
			return 0, errors.Wrap(err, "parsePagesDict: can't locate Pagedict or Pagesdict")
		}

		pageNodeDict := Object.(Dict)
		dictType := pageNodeDict.Type()
		if dictType == nil {
			return 0, errors.New("parsePagesDict: Missing dict type")
		}

		// Note: Pages may contain a to be inheritated ResourcesDict.

		if *dictType == "Pages" {

			// Recurse over pagetree and optimize resources.
			pageNumber, err = parsePagesDict(ctx, &pageNodeDict, pageNumber)
			if err != nil {
				return 0, err
			}

			continue
		}

		if *dictType != "Page" {
			return 0, errors.Errorf("parsePagesDict: Unexpected dict type: %s\n", *dictType)
		}

		// Mark page content streams for stats.
		err = identifyPageContent(ctx.XRefTable, &pageNodeDict, pageNumber, int(indRef.ObjectNumber))
		if err != nil {
			return 0, err
		}

		// Parse and optimize resource dict for one page.
		err = parseResourcesDict(ctx, &pageNodeDict, pageNumber, int(indRef.ObjectNumber))
		if err != nil {
			return 0, err
		}

		pageNumber++
	}

	log.Debug.Printf("parsePagesDict end: %s\n", *pagesDict)

	return pageNumber, nil
}

func traverse(xRefTable *XRefTable, value Object, duplObjs IntSet) error {

	if indRef, ok := value.(IndirectRef); ok {
		duplObjs[int(indRef.ObjectNumber)] = true
		o, err := xRefTable.Dereference(indRef)
		if err != nil {
			return err
		}
		traverseObjectGraphAndMarkDuplicates(xRefTable, o, duplObjs)
	}
	if dict, ok := value.(Dict); ok {
		traverseObjectGraphAndMarkDuplicates(xRefTable, dict, duplObjs)
	}
	if streamDict, ok := value.(StreamDict); ok {
		traverseObjectGraphAndMarkDuplicates(xRefTable, streamDict, duplObjs)
	}
	if arr, ok := value.(Array); ok {
		traverseObjectGraphAndMarkDuplicates(xRefTable, arr, duplObjs)
	}

	return nil
}

// Traverse the object graph for a Object and mark all objects as potential duplicates.
func traverseObjectGraphAndMarkDuplicates(xRefTable *XRefTable, obj Object, duplObjs IntSet) error {

	log.Debug.Printf("traverseObjectGraphAndMarkDuplicates begin type=%T\n", obj)

	switch x := obj.(type) {

	case Dict:
		log.Debug.Println("traverseObjectGraphAndMarkDuplicates: dict.")
		for _, value := range x {
			err := traverse(xRefTable, value, duplObjs)
			if err != nil {
				return err
			}
		}

	case StreamDict:
		log.Debug.Println("traverseObjectGraphAndMarkDuplicates: streamDict.")
		for _, value := range x.Dict {
			err := traverse(xRefTable, value, duplObjs)
			if err != nil {
				return err
			}
		}

	case Array:
		log.Debug.Println("traverseObjectGraphAndMarkDuplicates: arr.")
		for _, value := range x {
			err := traverse(xRefTable, value, duplObjs)
			if err != nil {
				return err
			}
		}
	}

	log.Debug.Println("traverseObjectGraphAndMarkDuplicates end")

	return nil
}

// Identify and mark all potential duplicate objects.
func calcRedundantObjects(ctx *Context) error {

	log.Debug.Println("calcRedundantObjects begin")

	for i, fontDict := range ctx.Optimize.DuplicateFonts {
		ctx.Optimize.DuplicateFontObjs[i] = true
		// Identify and mark all involved potential duplicate objects for a redundant font.
		err := traverseObjectGraphAndMarkDuplicates(ctx.XRefTable, *fontDict, ctx.Optimize.DuplicateFontObjs)
		if err != nil {
			return err
		}
	}

	for i, streamDict := range ctx.Optimize.DuplicateImages {
		ctx.Optimize.DuplicateImageObjs[i] = true
		// Identify and mark all involved potential duplicate objects for a redundant image.
		err := traverseObjectGraphAndMarkDuplicates(ctx.XRefTable, *streamDict, ctx.Optimize.DuplicateImageObjs)
		if err != nil {
			return err
		}
	}

	log.Debug.Println("calcRedundantObjects end")

	return nil
}

// Iterate over all pages and optimize resources.
// Get rid of duplicate embedded fonts and images.
func optimizeFontAndImages(ctx *Context) error {

	log.Debug.Println("optimizeFontAndImages begin")

	// Get a reference to the PDF indirect reference of the page tree root dict.
	indRefPages, err := ctx.Pages()
	if err != nil {
		return err
	}

	// Dereference and get a reference to the page tree root dict.
	pageTreeRootDict, err := ctx.XRefTable.DereferenceDict(*indRefPages)
	if err != nil {
		return err
	}

	// Detect the number of pages of this PDF file.
	pageCount := pageTreeRootDict.IntEntry("Count")
	if pageCount == nil {
		return errors.New("optimizeFontAndImagess: missing \"Count\" in page root dict")
	}

	// If PageCount already set by validation doublecheck.
	if ctx.PageCount > 0 && ctx.PageCount != *pageCount {
		return errors.New("optimizeFontAndImagess: unexpected page root dict pageCount discrepancy")
	}

	// If we optimize w/o prior validation, set PageCount.
	if ctx.PageCount == 0 {
		ctx.PageCount = *pageCount
	}

	// Prepare optimization environment.
	ctx.Optimize.PageFonts = make([]IntSet, ctx.PageCount)
	ctx.Optimize.PageImages = make([]IntSet, ctx.PageCount)

	// Iterate over page dicts and optimize resources.
	_, err = parsePagesDict(ctx, pageTreeRootDict, 0)
	if err != nil {
		return err
	}

	// Identify all duplicate objects.
	err = calcRedundantObjects(ctx)
	if err != nil {
		return err
	}

	log.Debug.Println("optimizeFontAndImages end")

	return nil
}

// Return stream length for font file object.
func streamLengthFontFile(xRefTable *XRefTable, indirectRef *IndirectRef) (*int64, error) {

	log.Debug.Println("streamLengthFontFile begin")

	objectNumber := indirectRef.ObjectNumber

	streamDict, err := xRefTable.DereferenceStreamDict(*indirectRef)
	if err != nil {
		return nil, err
	}

	if streamDict == nil || (*streamDict).StreamLength == nil {
		return nil, errors.Errorf("streamLengthFontFile: fontFile Streamlength is nil for object %d\n", objectNumber)
	}

	log.Debug.Println("streamLengthFontFile end")

	return (*streamDict).StreamLength, nil
}

// Calculate amount of memory used by embedded fonts for stats.
func calcEmbeddedFontsMemoryUsage(ctx *Context) error {

	log.Debug.Printf("calcEmbeddedFontsMemoryUsage begin: %d fontObjects\n", len(ctx.Optimize.FontObjects))

	fontFileIndRefs := map[IndirectRef]bool{}

	var objectNumbers []int

	// Sorting unnecessary.
	for k := range ctx.Optimize.FontObjects {
		objectNumbers = append(objectNumbers, k)
	}
	sort.Ints(objectNumbers)

	// Iterate over all embedded font objects and record font file references.
	for _, objectNumber := range objectNumbers {

		fontObject := ctx.Optimize.FontObjects[objectNumber]

		// Only embedded fonts have binary data.
		if !fontObject.Embedded() {
			continue
		}

		err := processFontFilesForFontDict(ctx.XRefTable, fontObject.FontDict, objectNumber, fontFileIndRefs)
		if err != nil {
			return err
		}
	}

	// Iterate over font file references and calculate total font size.
	for indRef := range fontFileIndRefs {
		streamLength, err := streamLengthFontFile(ctx.XRefTable, &indRef)
		if err != nil {
			return err
		}
		ctx.Read.BinaryFontSize += *streamLength
	}

	log.Debug.Println("calcEmbeddedFontsMemoryUsage end")

	return nil
}

// fontDescriptorFontFileIndirectObjectRef returns the indirect object for the font file for given font descriptor.
func fontDescriptorFontFileIndirectObjectRef(fontDescriptorDict *Dict) *IndirectRef {

	log.Debug.Println("fontDescriptorFontFileIndirectObjectRef begin")

	indirectRef := fontDescriptorDict.IndirectRefEntry("FontFile")

	if indirectRef == nil {
		indirectRef = fontDescriptorDict.IndirectRefEntry("FontFile2")
	}

	if indirectRef == nil {
		indirectRef = fontDescriptorDict.IndirectRefEntry("FontFile3")
	}

	if indirectRef == nil {
		//logInfoReader.Printf("FontDescriptorFontFileLength: FontDescriptor dict without fontFile: \n%s\n", fontDescriptorDict)
	}

	log.Debug.Println("FontDescriptorFontFileIndirectObjectRef end")

	return indirectRef
}

func trivialFontDescriptor(xRefTable *XRefTable, fontDict *Dict, objNr int) (*Dict, error) {

	obj, ok := fontDict.Find("FontDescriptor")
	if !ok {
		return nil, nil
	}

	// fontDescriptor directly available.

	dict, err := xRefTable.DereferenceDict(obj)
	if err != nil {
		return nil, err
	}

	if dict == nil {
		return nil, errors.Errorf("trivialFontDescriptor: FontDescriptor is null for font object %d\n", objNr)
	}

	if dict.Type() != nil && *dict.Type() != "FontDescriptor" {
		return nil, errors.Errorf("trivialFontDescriptor: FontDescriptor dict incorrect dict type for font object %d\n", objNr)
	}

	return dict, nil
}

// FontDescriptor gets the font descriptor for this font.
func fontDescriptor(xRefTable *XRefTable, fontDict *Dict, objNr int) (*Dict, error) {

	log.Debug.Println("fontDescriptor begin")

	dict, err := trivialFontDescriptor(xRefTable, fontDict, objNr)
	if err != nil {
		return nil, err
	}
	if dict != nil {
		return dict, nil
	}

	// Try to access a fontDescriptor in a Descendent font for Type0 fonts.

	obj, ok := fontDict.Find("DescendantFonts")
	if !ok {
		//logErrorOptimize.Printf("FontDescriptor: Neither FontDescriptor nor DescendantFonts for font object %d\n", objectNumber)
		return nil, nil
	}

	// A descendant font is contained in an array of size 1.

	arr, err := xRefTable.DereferenceArray(obj)
	if err != nil || arr == nil {
		return nil, errors.Errorf("fontDescriptor: DescendantFonts: IndirectRef or Array wth length 1 expected for font object %d\n", objNr)
	}

	if len(*arr) > 1 {
		return nil, errors.Errorf("fontDescriptor: DescendantFonts Array length > 1 %v\n", arr)
	}

	// dict is the fontDict of the descendant font.
	dict, err = xRefTable.DereferenceDict((*arr)[0])
	if err != nil {
		return nil, errors.Errorf("fontDescriptor: No descendant font dict for %v\n", arr)
	}

	if dict == nil {
		return nil, errors.Errorf("fontDescriptor: descendant font dict is null for %v\n", arr)
	}

	if *dict.Type() != "Font" {
		return nil, errors.Errorf("fontDescriptor: font dict with incorrect dict type for %v\n", dict)
	}

	obj, ok = (*dict).Find("FontDescriptor")
	if !ok {
		log.Debug.Printf("fontDescriptor: descendant font not embedded %s\n", dict)
		return nil, nil
	}

	dict, err = xRefTable.DereferenceDict(obj)
	if err != nil {
		return nil, errors.Errorf("fontDescriptor: No FontDescriptor dict for font object %d\n", objNr)
	}

	if dict == nil {
		return nil, errors.Errorf("fontDescriptor: FontDescriptor dict is null for font object %d\n", objNr)
	}

	if dict.Type() == nil {
		//logErrorOptimize.Printf("FontDescriptor: FontDescriptor without type \"FontDescriptor\" objNumber:%d\n", objNr)
	} else if *dict.Type() != "FontDescriptor" {
		return nil, errors.Errorf("fontDescriptor: FontDescriptor dict incorrect dict type for font object %d\n", objNr)
	}

	log.Debug.Println("fontDescriptor end")

	return dict, nil
}

// Record font file objects referenced by this fonts font descriptor for stats and size calculation.
func processFontFilesForFontDict(xRefTable *XRefTable, fontDict *Dict, objectNumber int, indRefsMap map[IndirectRef]bool) error {

	log.Debug.Println("processFontFilesForFontDict begin")

	// Note:
	// "ToUnicode" is also an entry containing binary content that could be inspected for duplicate content.

	dict, err := fontDescriptor(xRefTable, fontDict, objectNumber)
	if err != nil {
		return err
	}

	if dict != nil {
		if indRef := fontDescriptorFontFileIndirectObjectRef(dict); indRef != nil {
			indRefsMap[*indRef] = true
		}
	}

	log.Debug.Println("processFontFilesForFontDict end")

	return nil
}

// Calculate amount of memory used by duplicate embedded fonts for stats.
func calcRedundantEmbeddedFontsMemoryUsage(ctx *Context) error {

	log.Debug.Println("calcRedundantEmbeddedFontsMemoryUsage begin")

	fontFileIndRefs := map[IndirectRef]bool{}

	// Iterate over all duplicate fonts and record font file references.
	for objectNumber, fontDict := range ctx.Optimize.DuplicateFonts {

		// Duplicate Fonts have to be embedded, so no check here.
		if err := processFontFilesForFontDict(ctx.XRefTable, fontDict, objectNumber, fontFileIndRefs); err != nil {
			return err
		}

	}

	// Iterate over font file references and calculate total font size.
	for indRef := range fontFileIndRefs {

		streamLength, err := streamLengthFontFile(ctx.XRefTable, &indRef)
		if err != nil {
			return err
		}

		ctx.Read.BinaryFontDuplSize += *streamLength
	}

	log.Debug.Println("calcRedundantEmbeddedFontsMemoryUsage end")

	return nil
}

// Calculate amount of memory used by embedded fonts and duplicate embedded fonts for stats.
func calcFontBinarySizes(ctx *Context) error {

	log.Debug.Println("calcFontBinarySizes begin")

	err := calcEmbeddedFontsMemoryUsage(ctx)
	if err != nil {
		return err
	}

	err = calcRedundantEmbeddedFontsMemoryUsage(ctx)
	if err != nil {
		return err
	}

	log.Debug.Println("calcFontBinarySizes end")

	return nil
}

// Calculate amount of memory used by images and duplicate images for stats.
func calcImageBinarySizes(ctx *Context) {

	log.Debug.Println("calcImageBinarySizes begin")

	// Calc memory usage for images.
	for _, imageObject := range ctx.Optimize.ImageObjects {
		ctx.Read.BinaryImageSize += *imageObject.ImageDict.StreamLength
	}

	// Calc memory usage for duplicate images.
	for _, imageDict := range ctx.Optimize.DuplicateImages {
		ctx.Read.BinaryImageDuplSize += *imageDict.StreamLength
	}

	log.Debug.Println("calcImageBinarySizes end")
}

// Calculate memory usage of binary data for stats.
func calcBinarySizes(ctx *Context) error {

	log.Debug.Println("calcBinarySizes begin")

	// Calculate font memory usage for stats.
	err := calcFontBinarySizes(ctx)
	if err != nil {
		return err
	}

	// Calculate image memory usage for stats.
	calcImageBinarySizes(ctx)

	// Note: Content streams also represent binary content.

	log.Debug.Println("calcBinarySizes end")

	return nil
}

// OptimizeXRefTable optimizes an xRefTable by locating and getting rid of redundant embedded fonts and images.
func OptimizeXRefTable(ctx *Context) error {

	log.Info.Println("optimizing fonts & images")

	log.Debug.Println("optimizeXRefTable begin")

	// Get rid of duplicate embedded fonts and images.
	err := optimizeFontAndImages(ctx)
	if err != nil {
		return err
	}

	// Calculate memory usage of binary content for stats.
	err = calcBinarySizes(ctx)
	if err != nil {
		return err
	}

	ctx.Optimized = true

	log.Debug.Println("optimizeXRefTable end")

	return nil
}
