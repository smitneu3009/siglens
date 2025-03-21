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

package segresults

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dustin/go-humanize"
	dtu "github.com/siglens/siglens/pkg/common/dtypeutils"
	"github.com/siglens/siglens/pkg/segment/aggregations"
	"github.com/siglens/siglens/pkg/segment/reader/segread"
	"github.com/siglens/siglens/pkg/segment/results/blockresults"
	"github.com/siglens/siglens/pkg/segment/structs"
	"github.com/siglens/siglens/pkg/segment/utils"
	"github.com/siglens/siglens/pkg/segment/writer/stats"
	log "github.com/sirupsen/logrus"
)

type EarlyExitType uint8

const EMPTY_GROUPBY_KEY = "*"

const (
	EetContSearch EarlyExitType = iota + 1
	EetMatchAllAggs
	EetEarlyExit
)

func (e EarlyExitType) String() string {
	switch e {
	case EetContSearch:
		return "Continue search"
	case EetMatchAllAggs:
		return "Match all aggs"
	case EetEarlyExit:
		return "Early exit"
	default:
		return fmt.Sprintf("Unknown early exit type %d", e)
	}
}

// Stores information received by remote nodes for a query
type remoteSearchResult struct {

	// for RRCs in BlockResults that come from remote nodes, this map stores the raw logs
	remoteLogs map[string]map[string]interface{}

	// all columns that are present in the remote logs
	remoteColumns map[string]struct{}
}

// exposes a struct to manage and maintain thread safe addition of results
type SearchResults struct {
	updateLock *sync.Mutex
	queryType  structs.QueryType
	qid        uint64
	sAggs      *structs.QueryAggregators
	sizeLimit  uint64

	resultCount  uint64 // total count of results
	EarlyExit    bool
	BlockResults *blockresults.BlockResults // stores information about the matched RRCs
	remoteInfo   *remoteSearchResult        // stores information about remote raw logs and columns

	runningSegStat   []*structs.SegStats
	runningEvalStats map[string]interface{}
	segStatsResults  *segStatsResults
	convertedBuckets map[string]*structs.AggregationResult
	allSSTS          map[uint16]map[string]*structs.SegStats // maps segKeyEnc to a map of segstats
	AllErrors        []error
	SegKeyToEnc      map[string]uint16
	SegEncToKey      map[uint16]string
	MaxSegKeyEnc     uint16
	ColumnsOrder     map[string]int

	statsAreFinal bool // If true, segStatsResults and convertedBuckets must not change.
}

type segStatsResults struct {
	measureResults   map[string]utils.CValueEnclosure // maps agg function to result
	measureFunctions []string
	groupByCols      []string
}

func InitSearchResults(sizeLimit uint64, aggs *structs.QueryAggregators, qType structs.QueryType, qid uint64) (*SearchResults, error) {
	lock := &sync.Mutex{}
	blockResults, err := blockresults.InitBlockResults(sizeLimit, aggs, qid)
	if err != nil {
		log.Errorf("InitSearchResults: failed to initialize blockResults: %v, qid=%v", err, qid)
		return nil, err
	}

	allErrors := make([]error, 0)
	var runningSegStat []*structs.SegStats
	if aggs != nil && aggs.MeasureOperations != nil {
		runningSegStat = make([]*structs.SegStats, len(aggs.MeasureOperations))
	}
	return &SearchResults{
		queryType:    qType,
		updateLock:   lock,
		sizeLimit:    sizeLimit,
		resultCount:  uint64(0),
		BlockResults: blockResults,
		remoteInfo: &remoteSearchResult{
			remoteLogs:    make(map[string]map[string]interface{}),
			remoteColumns: make(map[string]struct{}),
		},
		qid:              qid,
		sAggs:            aggs,
		allSSTS:          make(map[uint16]map[string]*structs.SegStats),
		runningSegStat:   runningSegStat,
		runningEvalStats: make(map[string]interface{}),
		AllErrors:        allErrors,
		SegKeyToEnc:      make(map[string]uint16),
		SegEncToKey:      make(map[uint16]string),
		MaxSegKeyEnc:     1,
	}, nil
}

func (sr *SearchResults) InitSegmentStatsResults(mOps []*structs.MeasureAggregator) {
	sr.updateLock.Lock()
	mFuncs := make([]string, len(mOps))
	for i, op := range mOps {
		mFuncs[i] = op.String()
	}
	retVal := make(map[string]utils.CValueEnclosure, len(mOps))
	sr.segStatsResults = &segStatsResults{
		measureResults:   retVal,
		measureFunctions: mFuncs,
	}
	sr.updateLock.Unlock()
}

// checks if total count has been set and if any more raw records are needed
// if retruns true, then only aggregations / sorts are needed
func (sr *SearchResults) ShouldContinueRRCSearch() bool {
	return sr.resultCount <= sr.sizeLimit
}

// Adds local results to the search results
func (sr *SearchResults) AddBlockResults(blockRes *blockresults.BlockResults) {
	sr.updateLock.Lock()
	for _, rec := range blockRes.GetResults() {
		_, removedID := sr.BlockResults.Add(rec)
		sr.removeLog(removedID)
	}
	sr.resultCount += blockRes.MatchedCount
	sr.BlockResults.MergeBuckets(blockRes)
	sr.updateLock.Unlock()
}

// returns the raw, running buckets that have been created. This is used to merge with remote results
func (sr *SearchResults) GetRunningBuckets() (*blockresults.TimeBuckets, *blockresults.GroupByBuckets) {
	return sr.BlockResults.TimeAggregation, sr.BlockResults.GroupByAggregation
}

/*
adds an entry to the remote logs

the caller is responsible for ensuring that
*/
func (sr *SearchResults) addRawLog(id string, log map[string]interface{}) {
	sr.remoteInfo.remoteLogs[id] = log
}

/*
Removes an entry from the remote logs
*/
func (sr *SearchResults) removeLog(id string) {
	if id == "" {
		return
	}
	delete(sr.remoteInfo.remoteLogs, id)
}

func (sr *SearchResults) AddSSTMap(sstMap map[string]*structs.SegStats, skEnc uint16) {
	sr.updateLock.Lock()
	sr.allSSTS[skEnc] = sstMap
	sr.updateLock.Unlock()
}

// deletes segKeyEnc from the map of allSSTS and any errors associated with it
func (sr *SearchResults) GetEncodedSegStats(segKeyEnc uint16) ([]byte, error) {
	sr.updateLock.Lock()
	retVal, ok := sr.allSSTS[segKeyEnc]
	delete(sr.allSSTS, segKeyEnc)
	sr.updateLock.Unlock()
	if !ok {
		return nil, nil
	}

	allSegStatJson := make(map[string]*structs.SegStatsJSON, len(retVal))
	for k, v := range retVal {
		rawJson, err := v.ToJSON()
		if err != nil {
			log.Errorf("GetEncodedSegStats: failed to convert segstats to json, qid=%v, err: %v", sr.qid, err)
			continue
		}
		allSegStatJson[k] = rawJson
	}
	allJSON := &structs.AllSegStatsJSON{
		AllSegStats: allSegStatJson,
	}
	jsonBytes, err := json.Marshal(allJSON)
	if err != nil {
		log.Errorf("GetEncodedSegStats: failed to marshal allSegStatJson, qid=%v, err: %v", sr.qid, err)
		return nil, err
	}
	return jsonBytes, nil
}

func (sr *SearchResults) AddError(err error) {
	sr.updateLock.Lock()
	sr.AllErrors = append(sr.AllErrors, err)
	sr.updateLock.Unlock()
}

func (sr *SearchResults) UpdateSegmentStats(sstMap map[string]*structs.SegStats, measureOps []*structs.MeasureAggregator) error {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	for idx, measureAgg := range measureOps {
		if len(sstMap) == 0 {
			continue
		}

		aggOp := measureAgg.MeasureFunc
		aggCol := measureAgg.MeasureCol

		if aggOp == utils.Count && aggCol == "*" {
			// Choose the first column.
			for key := range sstMap {
				aggCol = key
				break
			}
		}
		currSst, ok := sstMap[aggCol]
		if !ok && measureAgg.ValueColRequest == nil {
			log.Debugf("UpdateSegmentStats: sstMap was nil for aggCol %v, qid=%v", aggCol, sr.qid)
			continue
		}
		var err error
		var sstResult *utils.NumTypeEnclosure
		// For eval statements in aggregate functions, there should be only one field for min and max
		switch aggOp {
		case utils.Min:
			if measureAgg.ValueColRequest != nil {
				err := aggregations.ComputeAggEvalForMinOrMax(measureAgg, sstMap, sr.segStatsResults.measureResults, true)
				if err != nil {
					return fmt.Errorf("UpdateSegmentStats: qid=%v, err: %v", sr.qid, err)
				}
				continue
			}
			sstResult, err = segread.GetSegMin(sr.runningSegStat[idx], currSst)
		case utils.Max:
			if measureAgg.ValueColRequest != nil {
				err := aggregations.ComputeAggEvalForMinOrMax(measureAgg, sstMap, sr.segStatsResults.measureResults, false)
				if err != nil {
					return fmt.Errorf("UpdateSegmentStats: qid=%v, err: %v", sr.qid, err)
				}
				continue
			}
			sstResult, err = segread.GetSegMax(sr.runningSegStat[idx], currSst)
		case utils.Range:
			if measureAgg.ValueColRequest != nil {
				err := aggregations.ComputeAggEvalForRange(measureAgg, sstMap, sr.segStatsResults.measureResults, sr.runningEvalStats)
				if err != nil {
					return fmt.Errorf("UpdateSegmentStats: qid=%v, err: %v", sr.qid, err)
				}
				continue
			}
			sstResult, err = segread.GetSegRange(sr.runningSegStat[idx], currSst)
		case utils.Cardinality:
			if measureAgg.ValueColRequest != nil {
				err := aggregations.ComputeAggEvalForCardinality(measureAgg, sstMap, sr.segStatsResults.measureResults, sr.runningEvalStats)
				if err != nil {
					return fmt.Errorf("UpdateSegmentStats: qid=%v, err: %v", sr.qid, err)
				}
				continue
			}
			sstResult, err = segread.GetSegCardinality(sr.runningSegStat[idx], currSst)
		case utils.Count:
			if measureAgg.ValueColRequest != nil {
				err := aggregations.ComputeAggEvalForCount(measureAgg, sstMap, sr.segStatsResults.measureResults)
				if err != nil {
					return fmt.Errorf("UpdateSegmentStats: qid=%v, err: %v", sr.qid, err)
				}
				continue
			}
			sstResult, err = segread.GetSegCount(sr.runningSegStat[idx], currSst)
		case utils.Sum:
			if measureAgg.ValueColRequest != nil {
				err := aggregations.ComputeAggEvalForSum(measureAgg, sstMap, sr.segStatsResults.measureResults)
				if err != nil {
					return fmt.Errorf("UpdateSegmentStats: qid=%v, err: %v", sr.qid, err)
				}
				continue
			}
			sstResult, err = segread.GetSegSum(sr.runningSegStat[idx], currSst)
		case utils.Avg:
			if measureAgg.ValueColRequest != nil {
				err := aggregations.ComputeAggEvalForAvg(measureAgg, sstMap, sr.segStatsResults.measureResults, sr.runningEvalStats)
				if err != nil {
					return fmt.Errorf("UpdateSegmentStats: qid=%v, err: %v", sr.qid, err)
				}
				continue
			}
			sstResult, err = segread.GetSegAvg(sr.runningSegStat[idx], currSst)
		case utils.Values:
			strSet := make(map[string]struct{}, 0)
			valuesStrSetVal, exists := sr.runningEvalStats[measureAgg.String()]
			if !exists {
				sr.runningEvalStats[measureAgg.String()] = strSet
			} else {
				strSet, ok = valuesStrSetVal.(map[string]struct{})
				if !ok {
					return fmt.Errorf("UpdateSegmentStats: can not convert strSet for aggCol: %v, qid=%v", measureAgg.String(), sr.qid)
				}
			}

			if measureAgg.ValueColRequest != nil {
				err := aggregations.ComputeAggEvalForValues(measureAgg, sstMap, sr.segStatsResults.measureResults, strSet)
				if err != nil {
					return fmt.Errorf("UpdateSegmentStats: qid=%v, err: %v", sr.qid, err)
				}
				continue
			}

			// Merge two SegStat
			if currSst != nil && currSst.StringStats != nil && currSst.StringStats.StrSet != nil {
				for str := range currSst.StringStats.StrSet {
					strSet[str] = struct{}{}
				}
			}
			if sr.runningSegStat[idx] != nil {

				for str := range sr.runningSegStat[idx].StringStats.StrSet {
					strSet[str] = struct{}{}
				}

				sr.runningSegStat[idx].StringStats.StrSet = strSet
			}

			sr.runningEvalStats[measureAgg.String()] = strSet

			uniqueStrings := make([]string, 0)
			for str := range strSet {
				uniqueStrings = append(uniqueStrings, str)
			}
			sort.Strings(uniqueStrings)

			sr.segStatsResults.measureResults[measureAgg.String()] = utils.CValueEnclosure{
				Dtype: utils.SS_DT_STRING_SLICE,
				CVal:  uniqueStrings,
			}
			continue
		case utils.List:
			if measureAgg.ValueColRequest != nil {
				err := aggregations.ComputeAggEvalForList(measureAgg, sstMap, sr.segStatsResults.measureResults, sr.runningEvalStats)
				if err != nil {
					return fmt.Errorf("UpdateSegmentStats: qid=%v, err: %v", sr.qid, err)
				}
				continue
			}
			res, err := segread.GetSegList(sr.runningSegStat[idx], currSst)
			if err != nil {
				log.Errorf("UpdateSegmentStats: error getting segment level stats %+v, qid=%v", err, sr.qid)
				return err
			}
			sr.segStatsResults.measureResults[measureAgg.String()] = *res
			if sr.runningSegStat[idx] == nil {
				sr.runningSegStat[idx] = currSst
			}
			continue
		default:
			log.Errorf("UpdateSegmentStats: does not support using aggOps: %v, qid=%v", aggOp, sr.qid)
			return err
		}
		if err != nil {
			log.Errorf("UpdateSegmentStats: error getting segment level stats %+v, qid=%v", err, sr.qid)
			return err
		}

		// if this is the first segment, then set the running segment stat to the current segment stat
		// else, segread.GetN will update the running segment stat
		if sr.runningSegStat[idx] == nil {
			sr.runningSegStat[idx] = currSst
		}

		enclosure, err := sstResult.ToCValueEnclosure()
		if err != nil {
			log.Errorf("UpdateSegmentStats: cannot convert sstResult: %v, qid=%v", err, sr.qid)
			return err
		}
		sr.segStatsResults.measureResults[measureAgg.String()] = *enclosure
	}
	return nil
}

func (sr *SearchResults) GetQueryCount() *structs.QueryCount {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	var shouldEarlyExit bool
	if sr.sAggs != nil {
		shouldEarlyExit = sr.sAggs.EarlyExit
	} else {
		shouldEarlyExit = true
	}
	qc := &structs.QueryCount{TotalCount: sr.resultCount, EarlyExit: shouldEarlyExit}
	if sr.EarlyExit {
		qc.Op = utils.GreaterThanOrEqualTo
	} else {
		qc.Op = utils.Equals
	}
	return qc
}

func (sr *SearchResults) GetTotalCount() uint64 {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	return sr.resultCount
}

func (sr *SearchResults) GetAggs() *structs.QueryAggregators {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	return sr.sAggs
}

// Adds remote rrc results to the search results
func (sr *SearchResults) MergeRemoteRRCResults(rrcs []*utils.RecordResultContainer, grpByBuckets *blockresults.GroupByBucketsJSON,
	timeBuckets *blockresults.TimeBucketsJSON, allCols map[string]struct{}, rawLogs []map[string]interface{},
	remoteCount uint64, earlyExit bool) error {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	for cName := range allCols {
		sr.remoteInfo.remoteColumns[cName] = struct{}{}
	}

	for idx, rrc := range rrcs {
		addedRRC, removedID := sr.BlockResults.Add(rrc)
		if addedRRC {
			sr.addRawLog(rrc.SegKeyInfo.RecordId, rawLogs[idx])
		}
		sr.removeLog(removedID)
	}
	if earlyExit {
		sr.EarlyExit = true
	}
	err := sr.BlockResults.MergeRemoteBuckets(grpByBuckets, timeBuckets)
	if err != nil {
		log.Errorf("MergeRemoteRRCResults: Error merging remote buckets, qid=%v, err: %v", sr.qid, err)
		return err
	}
	sr.resultCount += remoteCount
	return nil
}

func (sr *SearchResults) AddSegmentStats(allJSON *structs.AllSegStatsJSON) error {
	sstMap := make(map[string]*structs.SegStats, len(allJSON.AllSegStats))
	for k, v := range allJSON.AllSegStats {
		rawStats, err := v.ToStats()
		if err != nil {
			return err
		}
		sstMap[k] = rawStats
	}
	return sr.UpdateSegmentStats(sstMap, sr.sAggs.MeasureOperations)
}

// Get remote raw logs and columns based on the remoteID and all RRCs
func (sr *SearchResults) GetRemoteInfo(remoteID string, inrrcs []*utils.RecordResultContainer) ([]map[string]interface{}, []string, error) {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	if sr.remoteInfo == nil {
		return nil, nil, fmt.Errorf("GetRemoteInfo: log does not have remote info, qid=%v", sr.qid)
	}
	finalLogs := make([]map[string]interface{}, 0, len(inrrcs))
	rawLogs := sr.remoteInfo.remoteLogs
	remoteCols := sr.remoteInfo.remoteColumns
	count := 0
	for i := 0; i < len(inrrcs); i++ {
		if inrrcs[i].SegKeyInfo.IsRemote && strings.HasPrefix(inrrcs[i].SegKeyInfo.RecordId, remoteID) {
			finalLogs = append(finalLogs, rawLogs[inrrcs[i].SegKeyInfo.RecordId])
			count++
		}
	}
	finalLogs = finalLogs[:count]

	allCols := make([]string, 0, len(remoteCols))
	idx := 0
	for col := range remoteCols {
		allCols = append(allCols, col)
		idx++
	}
	allCols = allCols[:idx]
	sort.Strings(allCols)
	return finalLogs, allCols, nil
}

func (sr *SearchResults) GetSegmentStatsResults(skEnc uint16) ([]*structs.BucketHolder, []string, []string, []string, int) {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()

	if sr.segStatsResults == nil {
		return nil, nil, nil, nil, 0
	}
	delete(sr.allSSTS, skEnc)
	bucketHolder := &structs.BucketHolder{}
	bucketHolder.MeasureVal = make(map[string]interface{})
	bucketHolder.GroupByValues = []string{EMPTY_GROUPBY_KEY}
	for mfName, aggVal := range sr.segStatsResults.measureResults {
		switch aggVal.Dtype {
		case utils.SS_DT_FLOAT:
			bucketHolder.MeasureVal[mfName] = humanize.CommafWithDigits(aggVal.CVal.(float64), 3)
		case utils.SS_DT_SIGNED_NUM:
			bucketHolder.MeasureVal[mfName] = humanize.Comma(aggVal.CVal.(int64))
		case utils.SS_DT_STRING:
			bucketHolder.MeasureVal[mfName] = aggVal.CVal
		case utils.SS_DT_STRING_SLICE:
			strVal, err := aggVal.GetString()
			if err != nil {
				log.Errorf("GetSegmentStatsResults: failed to convert string slice to string, qid: %v, err: %v", sr.qid, err)
				bucketHolder.MeasureVal[mfName] = ""
			} else {
				bucketHolder.MeasureVal[mfName] = strVal
			}
		}
	}
	aggMeasureResult := []*structs.BucketHolder{bucketHolder}
	return aggMeasureResult, sr.segStatsResults.measureFunctions, sr.segStatsResults.groupByCols, nil, len(sr.segStatsResults.measureResults)
}

func (sr *SearchResults) GetSegmentStatsMeasureResults() map[string]utils.CValueEnclosure {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	return sr.segStatsResults.measureResults
}

func (sr *SearchResults) GetSegmentRunningStats() []*structs.SegStats {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	return sr.runningSegStat
}

func (sr *SearchResults) GetGroupyByBuckets(limit int) ([]*structs.BucketHolder, []string, []string, map[string]int, int) {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()

	if sr.convertedBuckets != nil && !sr.statsAreFinal {
		sr.loadBucketsInternal()
	}

	bucketHolderArr, retMFuns, added := CreateMeasResultsFromAggResults(limit,
		sr.convertedBuckets)

	if sr.sAggs == nil || sr.sAggs.GroupByRequest == nil {
		return bucketHolderArr, retMFuns, nil, make(map[string]int), added
	} else {
		return bucketHolderArr, retMFuns, sr.sAggs.GroupByRequest.GroupByColumns, sr.ColumnsOrder, added
	}
}

// If agg.GroupByRequest.GroupByColumns == StatisticExpr.GroupByCols, which means there is only one groupby block in query
func (sr *SearchResults) IsOnlyStatisticGroupBy() bool {
	for agg := sr.sAggs; agg != nil; agg = agg.Next {
		if agg.GroupByRequest != nil && agg.GroupByRequest.GroupByColumns != nil {
			for _, groupByCol1 := range agg.GroupByRequest.GroupByColumns {
				for _, groupByCol2 := range sr.GetStatisticGroupByCols() {
					if groupByCol1 != groupByCol2 {
						return false
					}
				}
			}
			return true
		}
	}
	return false
}

func (sr *SearchResults) GetStatisticGroupByCols() []string {
	groupByCols := make([]string, 0)
	for agg := sr.sAggs; agg != nil; agg = agg.Next {
		if agg.OutputTransforms != nil && agg.OutputTransforms.LetColumns != nil && agg.OutputTransforms.LetColumns.StatisticColRequest != nil {
			groupByCols = append(agg.OutputTransforms.LetColumns.StatisticColRequest.FieldList, agg.OutputTransforms.LetColumns.StatisticColRequest.ByClause...)
			return groupByCols
		}
	}
	return groupByCols
}

// Subsequent calls may not return the same result as the previous may clean up the underlying heap used. Use GetResultsCopy to prevent this
func (sr *SearchResults) GetResults() []*utils.RecordResultContainer {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	return sr.BlockResults.GetResults()
}

func (sr *SearchResults) GetResultsCopy() []*utils.RecordResultContainer {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	return sr.BlockResults.GetResultsCopy()
}

func (sr *SearchResults) GetBucketResults() map[string]*structs.AggregationResult {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()

	if !sr.statsAreFinal {
		sr.loadBucketsInternal()
	}

	return sr.convertedBuckets
}

func (sr *SearchResults) SetFinalStatsFromNodeResult(nodeResult *structs.NodeResult) error {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()

	if sr.statsAreFinal {
		return fmt.Errorf("SetFinalStatsFromNodeResult: stats are already final, qid=%v", sr.qid)
	}

	sr.ColumnsOrder = nodeResult.ColumnsOrder
	if len(nodeResult.GroupByCols) > 0 {
		sr.convertedBuckets = nodeResult.Histogram
	} else {
		if length := len(nodeResult.MeasureResults); length != 1 {
			err := fmt.Errorf("SetFinalStatsFromNodeResult: unexpected MeasureResults length, qid=%v", sr.qid)
			log.Errorf("SetFinalStatsFromNodeResult: qid=%v, err: %v", sr.qid, err)
			return err
		}

		sr.segStatsResults.measureFunctions = nodeResult.MeasureFunctions
		sr.segStatsResults.measureResults = make(map[string]utils.CValueEnclosure, len(nodeResult.MeasureFunctions))
		sr.segStatsResults.groupByCols = nil

		for _, measureFunc := range sr.segStatsResults.measureFunctions {
			value, ok := nodeResult.MeasureResults[0].MeasureVal[measureFunc]
			if !ok {
				err := fmt.Errorf("SetFinalStatsFromNodeResult: %v not found in MeasureVal, qid=%v", measureFunc, sr.qid)
				log.Errorf("SetFinalStatsFromNodeResult: qid=%v, err: %v", sr.qid, err)
				return err
			}

			// Create a CValueEnclosure for `value`.
			var valueAsEnclosure utils.CValueEnclosure
			valueStr, ok := value.(string)
			if !ok {
				err := fmt.Errorf("SetFinalStatsFromNodeResult: unexpected type: %T, qid=%v", value, sr.qid)
				log.Errorf("SetFinalStatsFromNodeResult: qid=%v, err: %v", sr.qid, err)
				return err
			}

			// Remove any commas.
			valueStr = strings.ReplaceAll(valueStr, ",", "")

			if valueFloat, err := strconv.ParseFloat(valueStr, 64); err == nil {
				valueAsEnclosure.Dtype = utils.SS_DT_FLOAT
				valueAsEnclosure.CVal = valueFloat
			} else if valueInt, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
				valueAsEnclosure.Dtype = utils.SS_DT_SIGNED_NUM
				valueAsEnclosure.CVal = valueInt
			} else {
				valueAsEnclosure.Dtype = utils.SS_DT_STRING
				valueAsEnclosure.CVal = valueStr
			}

			sr.segStatsResults.measureResults[measureFunc] = valueAsEnclosure
		}
	}

	sr.statsAreFinal = true

	return nil
}

func (sr *SearchResults) GetNumBuckets() int {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	retVal := 0
	if sr.BlockResults == nil {
		return 0
	}
	if sr.BlockResults.TimeAggregation != nil {
		retVal += len(sr.BlockResults.TimeAggregation.AllRunningBuckets)
	}
	if sr.BlockResults.GroupByAggregation != nil {
		retVal += len(sr.BlockResults.GroupByAggregation.AllRunningBuckets)
	}
	return retVal
}

func (sr *SearchResults) loadBucketsInternal() {
	if sr.statsAreFinal {
		log.Errorf("loadBucketsInternal: cannot update convertedBuckets because stats are final, qid %v", sr.qid)
		return
	}

	retVal := make(map[string]*structs.AggregationResult)
	if sr.BlockResults.TimeAggregation != nil {
		retVal[sr.sAggs.TimeHistogram.AggName] = sr.BlockResults.GetTimeBuckets()
	}
	if sr.BlockResults.GroupByAggregation != nil {
		retVal[sr.sAggs.GroupByRequest.AggName] = sr.BlockResults.GetGroupByBuckets()
	}
	sr.convertedBuckets = retVal
}

func (sr *SearchResults) GetAllErrors() []error {
	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()
	return sr.AllErrors
}

// returns if the segkey needs to be searched or if we have hit an early exit
func (sr *SearchResults) ShouldSearchSegKey(tRange *dtu.TimeRange,
	snt structs.SearchNodeType, otherAggsPresent bool, timeAggs bool) EarlyExitType {

	// do we have enough RRCs?
	if sr.ShouldContinueRRCSearch() {
		return EetContSearch
	}
	if sr.queryType == structs.GroupByCmd {
		if sr.GetNumBuckets() < sr.sAggs.GroupByRequest.BucketCount {
			return EetContSearch
		} else {
			return EetEarlyExit
		}
	} else if sr.queryType != structs.RRCCmd {
		return EetContSearch
	}

	// do the RRCs we have pass the sort check?
	if sr.sAggs != nil && sr.sAggs.Sort != nil {
		var willValBeAdded bool
		if sr.sAggs.Sort.Ascending {
			willValBeAdded = sr.BlockResults.WillValueBeAdded(float64(tRange.StartEpochMs))
		} else {
			willValBeAdded = sr.BlockResults.WillValueBeAdded(float64(tRange.EndEpochMs))
		}
		if willValBeAdded {
			return EetContSearch
		}
	}

	// do we have all sorted RRCs and now need to only run date histogram?
	if snt == structs.MatchAllQuery && timeAggs && !otherAggsPresent {
		return EetMatchAllAggs
	}

	// do we have all sorted RRCs but still need raw search to complete the rest of the buckets?
	if snt != structs.MatchAllQuery && (timeAggs || otherAggsPresent) {
		return EetContSearch
	}

	// do we have all sorted RRCs with no aggs?
	if sr.sAggs == nil {
		return EetEarlyExit
	}

	// do we have all sorted RRCs but should not early exit?
	if !sr.sAggs.EarlyExit {
		return EetContSearch
	}

	// do we have all sorted RRCs and now need to run aggregations?
	if sr.sAggs.TimeHistogram != nil {
		return EetContSearch
	}

	// do we have all sorted RRCs and now need to run aggregations?
	if sr.sAggs.GroupByRequest != nil {
		return EetContSearch
	}

	return EetEarlyExit
}

// returns true in following cases:
// 1. search is not rrc
// 1. if any value in a block will be added based on highTs and lowTs
// 2. if time buckets exist
func (sr *SearchResults) ShouldSearchRange(lowTs, highTs uint64) bool {
	if sr.queryType != structs.RRCCmd {
		return true
	}

	if sr.ShouldContinueRRCSearch() {
		return true
	}
	if sr.sAggs == nil {
		return false
	}
	if !sr.sAggs.EarlyExit {
		return true
	}
	if sr.sAggs.TimeHistogram != nil {
		return true
	}

	if sr.sAggs.Sort != nil {
		if sr.sAggs.Sort.Ascending {
			return sr.BlockResults.WillValueBeAdded(float64(lowTs))
		} else {
			return sr.BlockResults.WillValueBeAdded(float64(highTs))
		}
	}
	return false
}

// sets early exit to value
func (sr *SearchResults) SetEarlyExit(exited bool) {
	sr.EarlyExit = exited
}

func (sr *SearchResults) GetAddSegEnc(sk string) uint16 {

	sr.updateLock.Lock()
	defer sr.updateLock.Unlock()

	retval, ok := sr.SegKeyToEnc[sk]
	if ok {
		return retval
	}

	retval = sr.MaxSegKeyEnc
	sr.SegEncToKey[sr.MaxSegKeyEnc] = sk
	sr.SegKeyToEnc[sk] = sr.MaxSegKeyEnc
	sr.MaxSegKeyEnc++
	return retval
}

// helper struct to coordinate parallel segstats results
type StatsResults struct {
	rwLock  *sync.RWMutex
	ssStats map[string]*structs.SegStats // maps column name to segstats
}

func InitStatsResults() *StatsResults {
	return &StatsResults{
		rwLock:  &sync.RWMutex{},
		ssStats: make(map[string]*structs.SegStats),
	}
}

func (sr *StatsResults) MergeSegStats(m1 map[string]*structs.SegStats) {
	sr.rwLock.Lock()
	sr.ssStats = stats.MergeSegStats(sr.ssStats, m1)
	sr.rwLock.Unlock()
}

func (sr *StatsResults) GetSegStats() map[string]*structs.SegStats {
	sr.rwLock.Lock()
	retVal := sr.ssStats
	sr.rwLock.Unlock()
	return retVal
}

func CreateMeasResultsFromAggResults(limit int,
	aggRes map[string]*structs.AggregationResult) ([]*structs.BucketHolder, []string, int) {

	bucketHolderArr := make([]*structs.BucketHolder, 0)
	added := int(0)
	internalMFuncs := make(map[string]bool)
	for _, agg := range aggRes {
		for _, aggVal := range agg.Results {
			measureVal := make(map[string]interface{})
			groupByValues := make([]string, 0)
			for mName, mVal := range aggVal.StatRes {
				rawVal, err := mVal.GetValue()
				if err != nil {
					log.Errorf("CreateMeasResultsFromAggResults: failed to get raw value for measurement %+v", err)
					continue
				}
				internalMFuncs[mName] = true
				measureVal[mName] = rawVal

			}
			if added >= limit {
				break
			}
			switch bKey := aggVal.BucketKey.(type) {
			case float64, uint64, int64:
				bKeyConv := fmt.Sprintf("%+v", bKey)
				groupByValues = append(groupByValues, bKeyConv)
				added++
			case []string:

				for _, bk := range aggVal.BucketKey.([]string) {
					groupByValues = append(groupByValues, bk)
					added++
				}
			case string:
				groupByValues = append(groupByValues, bKey)
				added++
			case []interface{}:
				for _, bk := range aggVal.BucketKey.([]interface{}) {
					groupByValues = append(groupByValues, fmt.Sprintf("%+v", bk))
					added++
				}
			default:
				log.Errorf("CreateMeasResultsFromAggResults: Received an unknown type for bucket keyType! %T", bKey)
			}
			bucketHolder := &structs.BucketHolder{
				GroupByValues: groupByValues,
				MeasureVal:    measureVal,
			}
			bucketHolderArr = append(bucketHolderArr, bucketHolder)
		}
	}

	retMFuns := make([]string, len(internalMFuncs))
	idx := 0
	for mName := range internalMFuncs {
		retMFuns[idx] = mName
		idx++
	}

	return bucketHolderArr, retMFuns, added
}
