// Copyright (c) 2021-2024 SigScalr, Inc.
//
// This file is part of SigLens Observability Solution
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package writer

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	jsoniter "github.com/json-iterator/go"
	"github.com/siglens/siglens/pkg/config"
	"github.com/siglens/siglens/pkg/segment/structs"
	. "github.com/siglens/siglens/pkg/segment/structs"
	"github.com/siglens/siglens/pkg/segment/utils"
	"github.com/stretchr/testify/assert"
	bbp "github.com/valyala/bytebufferpool"
)

var cases = []struct {
	input string
}{
	{
		`{
					"a":"val1",
					"b":"val1",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val1",
					"b":"val1",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val1",
					"b":"val1",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val1",
					"b":"val1",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val2",
					"b":"val3",
					"c":false,
					"d":"Paul",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val1",
					"b":"val4",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val1",
					"b":"val2",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val1",
					"b":"val1",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"wow",
					"b":"val1",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 4
			}`,
	},
	{
		`{
					"a":"val1",
					"b":"val1",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val23",
					"b":"val1",
					"c":true,
					"d":"John",
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val1567",
					"b":"val1",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val1",
					"b":"val1",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"",
					"b":"val1",
					"c":true,
					"d":"John",
				   "e": 1,
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val1",
					"b":"val1",
					"c":true,
					"d":"John",
				   "f": 2
			}`,
	},
	{
		`{
					"a":"val1",
					"b":"val1",
					"c":true,
					"d":"John",
				   "f": 2
			}`,
	},
}

/*
func checkTree(t *testing.T, node1 *Node, node2 *Node) {
	assert.Equal(t, node1.aggValues, node2.aggValues)

	for key, child := range node1.children {
		otherChild, ok := node2.children[key]

		assert.True(t, ok)
		assert.Equal(t, child.matchedRecordsStartIndex, otherChild.matchedRecordsStartIndex)
		assert.Equal(t, child.matchedRecordsEndIndex, otherChild.matchedRecordsEndIndex)

		checkTree(t, child, otherChild)
	}
}

func check(t *testing.T, decTree StarTreeQueryMaker, groupByKeys []string, aggFunctions []*structs.MeasureAggregator,
	origTree *StarTree) {
	assert.Equal(t, groupByKeys, decTree.metadata.GroupByKeys)
	assert.Equal(t, aggFunctions, decTree.metadata.AggFunctions)

	checkTree(t, origTree.Root, decTree.tree.Root)

	assert.Equal(t, origTree.matchedRecordsIndices, decTree.tree.matchedRecordsIndices)
	}
*/

func TestStarTree(t *testing.T) {
	rangeIndex = map[string]*structs.Numbers{}

	var blockSummary structs.BlockSummary
	colWips := make(map[string]*ColWip)
	wipBlock := WipBlock{
		columnBlooms:       make(map[string]*BloomIndex),
		columnRangeIndexes: make(map[string]*RangeIndex),
		colWips:            colWips,
		columnsInBlock:     make(map[string]bool),
		blockSummary:       blockSummary,
		tomRollup:          make(map[uint64]*RolledRecs),
		tohRollup:          make(map[uint64]*RolledRecs),
		todRollup:          make(map[uint64]*RolledRecs),
		bb:                 bbp.Get(),
	}
	segstats := make(map[string]*SegStats)
	allCols := make(map[string]uint32)

	ss := NewSegStore(0)
	ss.wipBlock = wipBlock
	ss.SegmentKey = "test-segkey1"
	ss.AllSeenColumnSizes = allCols
	ss.pqTracker = initPQTracker()
	ss.AllSst = segstats
	ss.numBlocks = 0

	cnameCacheByteHashToStr := make(map[uint64]string)
	var jsParsingStackbuf [64]byte

	tsKey := config.GetTimeStampKey()
	for i, test := range cases {

		var record_json map[string]interface{}
		var json = jsoniter.ConfigCompatibleWithStandardLibrary
		decoder := json.NewDecoder(bytes.NewReader([]byte(test.input)))
		decoder.UseNumber()
		err := decoder.Decode(&record_json)
		if err != nil {
			t.Errorf("testid: %d: Failed to parse json err:%v", i+1, err)
			continue
		}
		raw, err := json.Marshal(record_json)
		assert.NoError(t, err)

		_, err = ss.EncodeColumns(raw, uint64(i), &tsKey, utils.SIGNAL_EVENTS,
			cnameCacheByteHashToStr, jsParsingStackbuf[:])
		assert.NoError(t, err)

		for _, cwip := range ss.wipBlock.colWips {
			ss.wipBlock.maxIdx = utils.MaxUint32(ss.wipBlock.maxIdx, cwip.cbufidx)
		}
		ss.wipBlock.blockSummary.RecCount += 1
	}

	groupByCols := []string{"a", "d"}
	mColNames := []string{"e", "f"}

	gcWorkBuf := make([][]string, len(groupByCols))
	for colNum := 0; colNum < len(groupByCols); colNum++ {
		gcWorkBuf[colNum] = make([]string, MaxAgileTreeNodeCountForAlloc)
	}

	builder := GetSTB().stbPtr

	for trial := 0; trial < 10; trial += 1 {
		builder.ResetSegTree(groupByCols, mColNames, gcWorkBuf)
		err := builder.ComputeStarTree(&ss.wipBlock)
		assert.NoError(t, err)
		root := builder.tree.Root

		_, err = builder.EncodeStarTree(ss.SegmentKey)
		assert.NoError(t, err)

		// first TotalMeasFns will be for col "e"
		agSumIdx := 1*(TotalMeasFns) + MeasFnSumIdx
		iv, err := root.aggValues[agSumIdx].Int64()
		assert.NoError(t, err)
		assert.Equal(t, iv,
			int64(34),
			fmt.Sprintf("expected sum of 34 for sum of column f; got %d",
				iv))

	}
	fName := fmt.Sprintf("%v.strl", ss.SegmentKey)
	_ = os.RemoveAll(fName)
	fName = fmt.Sprintf("%v.strm", ss.SegmentKey)
	_ = os.RemoveAll(fName)
}

func TestStarTreeMedium(t *testing.T) {
	rangeIndex = map[string]*structs.Numbers{}

	var largeCases []struct {
		input string
	}

	for i := 0; i < 1000; i += 1 {
		largeCases = append(largeCases, cases...)
	}

	currCases := largeCases

	var blockSummary structs.BlockSummary
	colWips := make(map[string]*ColWip)
	wipBlock := WipBlock{
		columnBlooms:       make(map[string]*BloomIndex),
		columnRangeIndexes: make(map[string]*RangeIndex),
		colWips:            colWips,
		columnsInBlock:     make(map[string]bool),
		blockSummary:       blockSummary,
		tomRollup:          make(map[uint64]*RolledRecs),
		tohRollup:          make(map[uint64]*RolledRecs),
		todRollup:          make(map[uint64]*RolledRecs),
		bb:                 bbp.Get(),
	}
	segstats := make(map[string]*SegStats)
	allCols := make(map[string]uint32)

	ss := NewSegStore(0)
	ss.wipBlock = wipBlock
	ss.SegmentKey = "test-segkey2"
	ss.AllSeenColumnSizes = allCols
	ss.pqTracker = initPQTracker()
	ss.AllSst = segstats
	ss.numBlocks = 0

	tsKey := config.GetTimeStampKey()

	cnameCacheByteHashToStr := make(map[uint64]string)
	var jsParsingStackbuf [64]byte

	for i, test := range currCases {

		var record_json map[string]interface{}
		var json = jsoniter.ConfigCompatibleWithStandardLibrary
		decoder := json.NewDecoder(bytes.NewReader([]byte(test.input)))
		decoder.UseNumber()
		err := decoder.Decode(&record_json)
		if err != nil {
			t.Errorf("testid: %d: Failed to parse json err:%v", i+1, err)
			continue
		}
		raw, err := json.Marshal(record_json)
		assert.NoError(t, err)

		_, err = ss.EncodeColumns(raw, uint64(i), &tsKey, utils.SIGNAL_EVENTS,
			cnameCacheByteHashToStr, jsParsingStackbuf[:])
		assert.NoError(t, err)

		for _, cwip := range ss.wipBlock.colWips {
			ss.wipBlock.maxIdx = utils.MaxUint32(ss.wipBlock.maxIdx, cwip.cbufidx)
		}
		ss.wipBlock.blockSummary.RecCount += 1
	}

	groupByCols := [...]string{"a", "d"}
	mColNames := []string{"e", "f"}

	gcWorkBuf := make([][]string, len(groupByCols))
	for colNum := 0; colNum < len(groupByCols); colNum++ {
		gcWorkBuf[colNum] = make([]string, MaxAgileTreeNodeCountForAlloc)
	}

	builder := GetSTB().stbPtr

	for trial := 0; trial < 10; trial += 1 {
		builder.ResetSegTree(groupByCols[:], mColNames, gcWorkBuf)
		err := builder.ComputeStarTree(&ss.wipBlock)
		assert.NoError(t, err)
		root := builder.tree.Root

		_, err = builder.EncodeStarTree(ss.SegmentKey)
		assert.NoError(t, err)

		// first TotalMeasFns will be for col "e"
		agSumIdx := 1*(TotalMeasFns) + MeasFnSumIdx
		iv, err := root.aggValues[agSumIdx].Int64()
		assert.NoError(t, err)
		assert.Equal(t, iv,
			int64(34*1000),
			fmt.Sprintf("expected sum of 340000 for sum of column f; got %d",
				iv))
	}
	fName := fmt.Sprintf("%v.strl", ss.SegmentKey)
	_ = os.RemoveAll(fName)
	fName = fmt.Sprintf("%v.strm", ss.SegmentKey)
	_ = os.RemoveAll(fName)
}

func TestStarTreeMediumEncoding(t *testing.T) {
	rangeIndex = map[string]*structs.Numbers{}

	var largeCases []struct {
		input string
	}

	for i := 0; i < 50; i += 1 {
		largeCases = append(largeCases, cases...)
	}

	currCases := largeCases

	var blockSummary structs.BlockSummary
	colWips := make(map[string]*ColWip)
	wipBlock := WipBlock{
		columnBlooms:       make(map[string]*BloomIndex),
		columnRangeIndexes: make(map[string]*RangeIndex),
		colWips:            colWips,
		columnsInBlock:     make(map[string]bool),
		blockSummary:       blockSummary,
		tomRollup:          make(map[uint64]*RolledRecs),
		tohRollup:          make(map[uint64]*RolledRecs),
		todRollup:          make(map[uint64]*RolledRecs),
		bb:                 bbp.Get(),
	}

	allCols := make(map[string]uint32)
	segstats := make(map[string]*SegStats)

	ss := NewSegStore(0)
	ss.wipBlock = wipBlock
	ss.SegmentKey = "test-segkey1"
	ss.AllSeenColumnSizes = allCols
	ss.pqTracker = initPQTracker()
	ss.AllSst = segstats
	ss.numBlocks = 0

	tsKey := config.GetTimeStampKey()

	cnameCacheByteHashToStr := make(map[uint64]string)
	var jsParsingStackbuf [64]byte

	for i, test := range currCases {

		var record_json map[string]interface{}
		var json = jsoniter.ConfigCompatibleWithStandardLibrary
		decoder := json.NewDecoder(bytes.NewReader([]byte(test.input)))
		decoder.UseNumber()
		err := decoder.Decode(&record_json)
		if err != nil {
			t.Errorf("testid: %d: Failed to parse json err:%v", i+1, err)
			continue
		}
		raw, err := json.Marshal(record_json)
		assert.NoError(t, err)

		_, err = ss.EncodeColumns(raw, uint64(i), &tsKey, utils.SIGNAL_EVENTS,
			cnameCacheByteHashToStr, jsParsingStackbuf[:])
		assert.NoError(t, err)

		for _, cwip := range ss.wipBlock.colWips {
			ss.wipBlock.maxIdx = utils.MaxUint32(ss.wipBlock.maxIdx, cwip.cbufidx)
		}
		ss.wipBlock.blockSummary.RecCount += 1
		ss.RecordCount++
	}

	groupByCols := [...]string{"a", "d"}
	mColNames := []string{"e", "f"}

	gcWorkBuf := make([][]string, len(groupByCols))
	for colNum := 0; colNum < len(groupByCols); colNum++ {
		gcWorkBuf[colNum] = make([]string, MaxAgileTreeNodeCountForAlloc)
	}

	builder := GetSTB().stbPtr

	for trial := 0; trial < 10; trial += 1 {
		builder.ResetSegTree(groupByCols[:], mColNames, gcWorkBuf)
		err := builder.ComputeStarTree(&ss.wipBlock)
		assert.NoError(t, err)
		root := builder.tree.Root

		_, err = builder.EncodeStarTree(ss.SegmentKey)
		assert.NoError(t, err)

		// first TotalMeasFns will be for col "e"
		agSumIdx := 1*(TotalMeasFns) + MeasFnSumIdx
		iv, err := root.aggValues[agSumIdx].Int64()
		assert.NoError(t, err)

		assert.Equal(t, iv,
			int64(1700),
			fmt.Sprintf("expected sum of 3400 for sum of column f; got %d",
				iv))

	}
	fName := fmt.Sprintf("%v.strl", ss.SegmentKey)
	_ = os.RemoveAll(fName)
	fName = fmt.Sprintf("%v.strm", ss.SegmentKey)
	_ = os.RemoveAll(fName)
}

func TestStarTreeMediumEncodingDecoding(t *testing.T) {
	rangeIndex = map[string]*structs.Numbers{}

	var largeCases []struct {
		input string
	}

	for i := 0; i < 50; i += 1 {
		largeCases = append(largeCases, cases...)
	}

	currCases := largeCases

	var blockSummary structs.BlockSummary
	colWips := make(map[string]*ColWip)
	wipBlock := WipBlock{
		columnBlooms:       make(map[string]*BloomIndex),
		columnRangeIndexes: make(map[string]*RangeIndex),
		colWips:            colWips,
		columnsInBlock:     make(map[string]bool),
		blockSummary:       blockSummary,
		tomRollup:          make(map[uint64]*RolledRecs),
		tohRollup:          make(map[uint64]*RolledRecs),
		todRollup:          make(map[uint64]*RolledRecs),
		bb:                 bbp.Get(),
	}
	segstats := make(map[string]*SegStats)
	allCols := make(map[string]uint32)

	ss := NewSegStore(0)
	ss.wipBlock = wipBlock
	ss.SegmentKey = "test-segkey4"
	ss.AllSeenColumnSizes = allCols
	ss.pqTracker = initPQTracker()
	ss.AllSst = segstats
	ss.numBlocks = 0

	tsKey := config.GetTimeStampKey()

	cnameCacheByteHashToStr := make(map[uint64]string)
	var jsParsingStackbuf [64]byte

	for i, test := range currCases {

		var record_json map[string]interface{}
		var json = jsoniter.ConfigCompatibleWithStandardLibrary
		decoder := json.NewDecoder(bytes.NewReader([]byte(test.input)))
		decoder.UseNumber()
		err := decoder.Decode(&record_json)
		if err != nil {
			t.Errorf("testid: %d: Failed to parse json err:%v", i+1, err)
			continue
		}
		raw, err := json.Marshal(record_json)
		assert.NoError(t, err)

		_, err = ss.EncodeColumns(raw, uint64(i), &tsKey, utils.SIGNAL_EVENTS,
			cnameCacheByteHashToStr, jsParsingStackbuf[:])
		assert.NoError(t, err)

		for _, cwip := range ss.wipBlock.colWips {
			ss.wipBlock.maxIdx = utils.MaxUint32(ss.wipBlock.maxIdx, cwip.cbufidx)
		}

		ss.wipBlock.blockSummary.RecCount += 1
	}

	groupByCols := [...]string{"a", "d"}
	mColNames := []string{"e", "f"}

	gcWorkBuf := make([][]string, len(groupByCols))
	for colNum := 0; colNum < len(groupByCols); colNum++ {
		gcWorkBuf[colNum] = make([]string, MaxAgileTreeNodeCountForAlloc)
	}

	builder := GetSTB().stbPtr

	for trial := 0; trial < 1; trial += 1 {
		builder.ResetSegTree(groupByCols[:], mColNames, gcWorkBuf)
		err := builder.ComputeStarTree(&ss.wipBlock)
		assert.NoError(t, err)
		root := builder.tree.Root

		_, err = builder.EncodeStarTree(ss.SegmentKey)
		assert.NoError(t, err)

		// first TotalMeasFns will be for col "e"
		agidx := 1*(TotalMeasFns) + MeasFnSumIdx
		iv, err := root.aggValues[agidx].Int64()
		assert.NoError(t, err)
		assert.Equal(t, int64(17*100), iv,
			fmt.Sprintf("expected 17000 for sum of column f; got %d",
				iv))

		agidx = 1*(TotalMeasFns) + MeasFnMinIdx
		iv, err = root.aggValues[agidx].Int64()
		assert.NoError(t, err)
		assert.Equal(t, int64(2), iv,
			fmt.Sprintf("expected 2 for min of column f; got %d",
				iv))

		agidx = 1*(TotalMeasFns) + MeasFnMaxIdx
		iv, err = root.aggValues[agidx].Int64()
		assert.NoError(t, err)
		assert.Equal(t, int64(4), iv,
			fmt.Sprintf("expected 4 for max of column f; got %d",
				iv))

		agidx = 1*(TotalMeasFns) + MeasFnCountIdx
		iv, err = root.aggValues[agidx].Int64()
		assert.NoError(t, err)
		assert.Equal(t, int64(800), iv,
			fmt.Sprintf("expected 800 for count of column f; got %d",
				iv))

	}
	fName := fmt.Sprintf("%v.strl", ss.SegmentKey)
	_ = os.RemoveAll(fName)
	fName = fmt.Sprintf("%v.strm", ss.SegmentKey)
	_ = os.RemoveAll(fName)
}
