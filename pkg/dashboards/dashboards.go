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

package dashboards

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/valyala/fasthttp"

	"github.com/siglens/siglens/pkg/blob"
	"github.com/siglens/siglens/pkg/config"
	"github.com/siglens/siglens/pkg/utils"
	log "github.com/sirupsen/logrus"
)

var defaultDashboardIds map[string]struct{}
var defaultDashboardNames map[string]struct{}

var allidsBaseFname string
var allDashIdsLock map[uint64]*sync.Mutex = make(map[uint64]*sync.Mutex)
var latestDashboardReadTimeMillis map[uint64]uint64

// map of "orgid" => "dashboardId" ==> "dashboardName"
// e.g. "1234567890" => "11812083241622924684" => "dashboard-1"
var allDashboardsIds map[uint64]map[string]string = make(map[uint64]map[string]string)
var allDashboardsIdsLock *sync.RWMutex = &sync.RWMutex{}

func readSavedDashboards(orgid uint64) ([]byte, error) {
	var dashboardData []byte
	allidsFname := getAllIdsFileName(orgid)
	_, err := os.Stat(allidsFname)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
	}

	dashboardData, err = os.ReadFile(allidsFname)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		log.Errorf("readSavedDashboards: Failed to read allidsFname file fname=%v, err=%v", allidsFname, err)
		return nil, err
	}

	allDashboardsIdsLock.Lock()
	if _, ok := allDashboardsIds[orgid]; !ok {
		allDashboardsIds[orgid] = make(map[string]string)
	}
	var allDashboardNames map[string]string
	err = json.Unmarshal(dashboardData, &allDashboardNames)
	if err != nil {
		allDashboardsIdsLock.Unlock()
		log.Errorf("readSavedDashboards: Failed to unmarshall allidsFname file fname=%v, err=%v", allidsFname, err)
		return nil, err
	}
	allDashboardsIds[orgid] = allDashboardNames
	latestDashboardReadTimeMillis[orgid] = utils.GetCurrentTimeInMs()
	allDashboardsIdsLock.Unlock()
	return dashboardData, nil
}

func readDefaultDashboards(orgid uint64) ([]byte, error) {
	var dashboardData []byte
	allidsFname := getDefaultDashboardFileName()

	dashboardData, err := os.ReadFile(allidsFname)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		log.Errorf("readDefaultDashboards: Failed to read allidsFname file fname=%v, err=%v", allidsFname, err)
		return nil, err
	}

	allDashboardsIdsLock.Lock()
	if _, ok := allDashboardsIds[orgid]; !ok {
		allDashboardsIds[orgid] = make(map[string]string)
	}
	var allDashboardNames map[string]string
	err = json.Unmarshal(dashboardData, &allDashboardNames)
	if err != nil {
		allDashboardsIdsLock.Unlock()
		log.Errorf("readDefaultDashboards: Failed to unmarshall allidsFname file fname=%v, err=%v, dashboardData=%v",
			allidsFname, err, dashboardData)
		return nil, err
	}
	allDashboardsIds[orgid] = allDashboardNames
	latestDashboardReadTimeMillis[orgid] = utils.GetCurrentTimeInMs()
	allDashboardsIdsLock.Unlock()
	return dashboardData, nil
}

func getAllIdsFileName(orgid uint64) string {
	var allidsFname string
	if orgid == 0 {
		allidsFname = allidsBaseFname + ".json"
	} else {
		allidsFname = allidsBaseFname + "-" + strconv.FormatUint(orgid, 10) + ".json"
	}
	return allidsFname
}

func getDefaultDashboardFileName() string {
	var allidsFname string
	var defaultDBsAllidsBaseFname string = "defaultDBs/allids"
	allidsFname = defaultDBsAllidsBaseFname + ".json"
	return allidsFname
}

func InitDashboards() error {
	var sb strings.Builder

	defaultDashboardNames = make(map[string]struct{})

	// Read the JSON file
	jsonData, err := os.ReadFile(getDefaultDashboardFileName())
	if err != nil {
		log.Errorf("InitDashboard: Failed to read default dashboards file, err=%v", err)
		return err
	}

	// Parse the JSON data
	var dashboards map[string]string
	err = json.Unmarshal(jsonData, &dashboards)
	if err != nil {
		log.Errorf("InitDashboard: Failed to unmarshal default dashboards, err=%v", err)
		return err
	}

	// Iterate over the parsed data and save dashboard names
	for _, name := range dashboards {
		defaultDashboardNames[name] = struct{}{}
	}

	defaultDashboardIds = make(map[string]struct{})

	defaultDashboardIds["10329b95-47a8-48df-8b1d-0a0a01ec6c42"] = struct{}{}
	defaultDashboardIds["a28f485c-4747-4024-bb6b-d230f101f852"] = struct{}{}
	defaultDashboardIds["bd74f11e-26c8-4827-bf65-c0b464e1f2a4"] = struct{}{}
	defaultDashboardIds["53cb3dde-fd78-4253-808c-18e4077ef0f1"] = struct{}{}

	sb.WriteString(config.GetDataPath() + "querynodes/" + config.GetHostID() + "/dashboards")
	baseDir := sb.String()
	allidsBaseFname = baseDir + "/allids"
	latestDashboardReadTimeMillis = make(map[uint64]uint64)

	err = os.MkdirAll(baseDir, 0764)
	if err != nil {
		log.Errorf("InitDashboard: failed to create basedir=%v, err=%v", baseDir, err)
		return err
	}

	err = os.MkdirAll(baseDir+"/details", 0764)
	if err != nil {
		log.Errorf("InitDashboard: failed to create basedir=%v, err=%v", baseDir, err)
		return err
	}

	createOrAcquireLock(0)
	_, err = readSavedDashboards(0)
	if err != nil {
		releaseLock(0)
		log.Errorf("InitDashboard: failed to read saved dashboards, err=%v", err)
		return err
	}
	releaseLock(0)

	return nil
}

func createOrAcquireLock(orgid uint64) {
	if _, ok := allDashIdsLock[orgid]; !ok {
		allDashIdsLock[orgid] = &sync.Mutex{}
	}
	allDashIdsLock[orgid].Lock()
}

func releaseLock(orgid uint64) {
	allDashIdsLock[orgid].Unlock()
}

func getAllDashboardIds(orgid uint64) (map[string]string, error) {
	createOrAcquireLock(orgid)
	_, err := readSavedDashboards(orgid)
	if err != nil {
		releaseLock(orgid)
		log.Errorf("getAllDashboardIds: failed to read, orgid=%v, err=%v", orgid, err)
		return nil, err
	}
	releaseLock(orgid)
	allDashboardsIdsLock.RLock()
	defer allDashboardsIdsLock.RUnlock()
	return allDashboardsIds[orgid], nil
}

func getAllDefaultDashboardIds(orgid uint64) (map[string]string, error) {
	createOrAcquireLock(orgid)
	_, err := readDefaultDashboards(orgid)
	if err != nil {
		releaseLock(orgid)
		log.Errorf("getAllDefaultDashboardIds: failed to read, orgid=%v,  err=%v", orgid, err)
		return nil, err
	}
	releaseLock(orgid)
	allDashboardsIdsLock.RLock()
	defer allDashboardsIdsLock.RUnlock()
	return allDashboardsIds[orgid], nil
}

// Generate the uniq uuid for the dashboard
func createUniqId(dname string) string {
	newId := uuid.New().String()
	return newId
}

// method to check if the dashboard name already exists
func dashboardNameExists(dname string, orgid uint64) bool {
	allDashboardIds, err := getAllDashboardIds(orgid)
	if err != nil {
		log.Errorf("dashboardNameExists: Error getting all dashboard IDs: %v", err)
		return false
	}
	for _, name := range allDashboardIds {
		if name == dname {
			return true
		}
	}
	return false
}

func createDashboard(dname string, orgid uint64) (map[string]string, error) {
	if dname == "" {
		log.Errorf("createDashboard: failed to create Dashboard, with empty dashboard name")
		return nil, errors.New("createDashboard: failed to create Dashboard, with empty dashboard name")
	}

	// Check if the dashboard name is a default name
	if _, isDefault := defaultDashboardNames[dname]; isDefault {
		log.Errorf("createDashboard: Dashboard with name %s is a default dashboard name and cannot be used", dname)
		return nil, errors.New("dashboard name already exists")
	}

	newId := createUniqId(dname)

	if dashboardNameExists(dname, orgid) {
		log.Errorf("createDashboard: Dashboard with name %s already exists", dname)
		return nil, errors.New("dashboard name already exists")
	}

	dashBoardIds, err := getAllDashboardIds(orgid)
	if err != nil {
		log.Errorf("createDashboard: Failed to get all dashboard ids err=%v", err)
		return nil, err
	}
	for _, dId := range dashBoardIds {
		if dId == newId {
			log.Errorf("createDashboard: Failed to create dashboard, dashboard id: %v already exists dname: %v", newId, dname)
			return nil, errors.New("createDashboard: Failed to create dashboard, dashboard id already exists")
		}
	}

	allDashboardsIdsLock.Lock()
	if _, ok := allDashboardsIds[orgid]; !ok {
		allDashboardsIds[orgid] = make(map[string]string)
	}
	allDashboardsIds[orgid][newId] = dname
	orgDashboards := allDashboardsIds[orgid]
	jdata, err := json.Marshal(&orgDashboards)
	allDashboardsIdsLock.Unlock()
	if err != nil {
		log.Errorf("createDashboard: Failed to marshall allDashboardids, dname: %v err=%v", dname, err)
		return nil, err
	}

	allidsFname := getAllIdsFileName(orgid)
	err = os.WriteFile(allidsFname, jdata, 0644)
	if err != nil {
		log.Errorf("createDashboard: Failed to write file=%v, err=%v", allidsFname, err)
		return nil, err
	}

	dashboardDetailsFname := config.GetDataPath() + "querynodes/" + config.GetHostID() + "/dashboards/details/" + newId + ".json"

	dData := []byte(fmt.Sprintf("{\"name\": \"%s\"}", dname))

	err = os.WriteFile(dashboardDetailsFname, dData, 0644)
	if err != nil {
		log.Errorf("createDashboard: Error creating empty local file %s: for dname: %v, err: %v",
			dashboardDetailsFname, dname, err)
		return nil, err
	}

	log.Infof("createDashboard: Successfully created file %v, for dname: %v", dashboardDetailsFname, dname)
	err = blob.UploadQueryNodeDir()
	if err != nil {
		log.Errorf("createDashboard: Failed to upload query nodes dir, dname: %v  err=%v", dname, err)
		return nil, err
	}

	retval := make(map[string]string)
	allDashboardsIdsLock.RLock()
	orgDashboardsIds := allDashboardsIds[orgid]
	allDashboardsIdsLock.RUnlock()

	retval[newId] = orgDashboardsIds[newId]

	return retval, nil
}

func isDefaultDashboard(id string) bool {

	_, exists := defaultDashboardIds[id]
	return exists
}

func toggleFavorite(id string) (bool, error) {
	// Load the dashboard JSON file
	var dashboardDetailsFname string
	if isDefaultDashboard(id) {
		dashboardDetailsFname = "defaultDBs/details/" + id + ".json"
	} else {
		dashboardDetailsFname = config.GetDataPath() + "querynodes/" + config.GetHostID() + "/dashboards/details/" + id + ".json"
	}
	dashboardJson, err := os.ReadFile(dashboardDetailsFname)
	if err != nil {
		log.Errorf("toggleFavorite: Failed to read file=%v, err=%v", dashboardDetailsFname, err)
		return false, err
	}

	// Unmarshal JSON file into a map
	var dashboard map[string]interface{}
	err = json.Unmarshal(dashboardJson, &dashboard)
	if err != nil {
		log.Errorf("toggleFavorite: Failed to unmarshal json, dashboardDetailsFname: %v, dashdata: %v, err: %v",
			dashboardDetailsFname, dashboard, err)
		return false, err
	}

	// Toggle the "isFavorite" field
	isFavorite, ok := dashboard["isFavorite"].(bool)
	if !ok {
		// If the "isFavorite" field does not exist or is not a bool, treat the dashboard as not favorited
		isFavorite = false
	}
	dashboard["isFavorite"] = !isFavorite

	// Marshal the updated dashboard back into JSON
	updatedDashboardJson, err := json.Marshal(dashboard)
	if err != nil {
		log.Errorf("toggleFavorite: Failed to marshal json, err=%v", err)
		return false, err
	}

	// Save the updated dashboard back to the JSON file
	err = os.WriteFile(dashboardDetailsFname, updatedDashboardJson, 0644)
	if err != nil {
		log.Errorf("toggleFavorite: Failed to write file=%v, err=%v", dashboardDetailsFname, err)
		return false, err
	}

	return !isFavorite, nil
}
func getDashboard(id string) (map[string]interface{}, error) {
	var dashboardDetailsFname string
	if isDefaultDashboard(id) {
		dashboardDetailsFname = "defaultDBs/details/" + id + ".json"
	} else {
		dashboardDetailsFname = config.GetDataPath() + "querynodes/" + config.GetHostID() + "/dashboards/details/" + id + ".json"
	}
	rdata, err := os.ReadFile(dashboardDetailsFname)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		log.Errorf("getDashboard: Failed to read dashboard file fname=%v, err=%v", dashboardDetailsFname, err)
		return nil, err
	}

	var detailDashboardInfo map[string]interface{} = make(map[string]interface{})

	err = json.Unmarshal(rdata, &detailDashboardInfo)
	if err != nil {
		log.Errorf("getDashboard: Failed to unmarshall dashboard file fname: %v, rdata: %v,  err: %v",
			dashboardDetailsFname, rdata, err)
		return nil, err
	}

	return detailDashboardInfo, nil
}

func updateDashboard(id string, dName string, dashboardDetails map[string]interface{}, orgid uint64) error {

	// Check if the dashboard exists
	allDashboards, err := getAllDashboardIds(orgid)
	if err != nil {
		log.Errorf("updateDashboard: Failed to get all dashboard ids err=%v", err)
		return err
	}
	_, ok := allDashboards[id]
	if !ok {
		log.Errorf("updateDashboard: Dashboard id %v does not exist, dname: %v", id, dName)
		return errors.New("updateDashboard: Dashboard id does not exist")
	}

	currentDashboardDetails, err := getDashboard(id)
	if err != nil {
		log.Errorf("updateDashboard: could not get id: %v, dname: %v, err=%v", id, dName, err)
		return errors.New("updateDashboard: Error fetching dashboard details")
	}

	// Check if isFavorite is provided in the update
	if _, exists := currentDashboardDetails["isFavorite"]; !exists {
		// If isFavorite does not exist in currentDashboardDetails, set it to false
		dashboardDetails["isFavorite"] = false
		currentDashboardDetails["isFavorite"] = false
	} else if _, ok := dashboardDetails["isFavorite"].(bool); !ok {
		// If isFavorite is not provided in the update, keep the current value
		dashboardDetails["isFavorite"] = currentDashboardDetails["isFavorite"]
	}
	// Update the dashboard name if it is different
	if allDashboards[id] != dName {
		if dashboardNameExists(dName, orgid) {
			log.Errorf("Dashboard with name %s already exists", dName)
			return errors.New("dashboard name already exists")
		} else {
			allDashboardsIds[orgid][id] = dName
		}
	}
	allDashboardsIdsLock.RLock()
	orgDashboards := allDashboardsIds[orgid]
	allDashboardsIdsLock.RUnlock()
	jdata, err := json.Marshal(&orgDashboards)
	if err != nil {
		log.Errorf("updateDashboard: Failed to marshall id: %v, dName: %v, data: %v, err=%v", id, dName, orgDashboards, err)
		return err
	}

	allidsFname := getAllIdsFileName(orgid)
	err = os.WriteFile(allidsFname, jdata, 0644)
	if err != nil {
		log.Errorf("updateDashboard: Failed to write file=%v, id: %v, dName: %v, err=%v", allidsFname, id, dName, err)
		return err
	}

	dashboardDetailsFname := config.GetDataPath() + "querynodes/" + config.GetHostID() + "/dashboards/details/" + id + ".json"

	jdata, err = json.Marshal(&dashboardDetails)
	if err != nil {
		log.Errorf("updateDashboard: Failed to marshall id: %v, dName: %v, data: %v,  err: %v", id, dName, dashboardDetails, err)
		return err
	}

	err = os.WriteFile(dashboardDetailsFname, jdata, 0644)
	if err != nil {
		log.Errorf("updateDashboard: Failed to writefile fname: %v, id: %v, dName: %v, err: %v", dashboardDetailsFname, id,
			dName, err)
		return err
	}
	log.Infof("updateDashboard: Successfully updated dashboard details in file %v", dashboardDetailsFname)

	// Update the query node dir
	err = blob.UploadQueryNodeDir()
	if err != nil {
		log.Errorf("updateDashboard: Failed to upload query nodes dir, id: %v, dName: %v, err: %v", id, dName, err)
		return err
	}

	return nil
}

func deleteDashboard(id string, orgid uint64) error {

	createOrAcquireLock(orgid)
	dashboardData, err := readSavedDashboards(orgid)
	if err != nil {
		releaseLock(orgid)
		log.Errorf("deleteDashboard: failed to read saved dashboards, err=%v", err)
		return err
	}
	releaseLock(orgid)

	var dashboardDetails map[string]string
	err = json.Unmarshal(dashboardData, &dashboardDetails)
	if err != nil {
		log.Errorf("deleteDashboard: Failed to unmarshall dashboard file for orgid=%v,dashboardData: %v, err=%v", orgid,
			dashboardData, err)
		return err
	}

	// Delete entry from dashboardInfo and write to file allids.json
	allDashboardsIdsLock.Lock()
	delete(allDashboardsIds[orgid], id)
	allDashboardsIdsLock.Unlock()

	// Update the file with latest dashboard info
	allDashboardsIdsLock.RLock()
	orgDashboardIds := allDashboardsIds[orgid]
	allDashboardsIdsLock.RUnlock()
	jdata, err := json.Marshal(&orgDashboardIds)
	if err != nil {
		log.Errorf("deleteDashboard: Failed to marshall, id: %v, data: %v err=%v", id, orgDashboardIds, err)
		return err
	}

	allidsFname := getAllIdsFileName(orgid)
	err = os.WriteFile(allidsFname, jdata, 0644)
	if err != nil {
		log.Errorf("deleteDashboard: Failed to write file: %v, err: %v", allidsFname, err)
		return err
	}

	// Delete dashboard details file
	dashboardDetailsFname := config.GetDataPath() + "querynodes/" + config.GetHostID() + "/dashboards/details/" + id + ".json"
	err = os.Remove(dashboardDetailsFname)
	if err != nil {
		log.Errorf("deleteDashboard:  Error deleting file %s: %v", dashboardDetailsFname, err)
		return err
	}

	// Update the query node dir
	err = blob.UploadQueryNodeDir()
	if err != nil {
		log.Errorf("deleteDashboard: Failed to upload query nodes dir  err=%v", err)
		return err
	}

	return nil
}

// method to set conflict message and 409 status code
func setConflictMsg(ctx *fasthttp.RequestCtx) {
	var httpResp utils.HttpServerResponse
	ctx.SetStatusCode(fasthttp.StatusConflict)
	httpResp.Message = "Conflict: Dashboard name already exists"
	httpResp.StatusCode = fasthttp.StatusConflict
	utils.WriteResponse(ctx, httpResp)
}

func ProcessCreateDashboardRequest(ctx *fasthttp.RequestCtx, myid uint64) {
	rawJSON := ctx.PostBody()
	if rawJSON == nil {
		log.Errorf("ProcessCreateDashboardRequest: received empty body id request")
		utils.SetBadMsg(ctx, "")
		return
	}

	var dname string

	err := json.Unmarshal(rawJSON, &dname)
	if err != nil {
		log.Errorf("ProcessCreateDashboardRequest: could not unmarshall body: %v, err=%v", rawJSON, err)
		utils.SetBadMsg(ctx, "")
		return
	}
	dashboardInfo, err := createDashboard(dname, myid)

	if err != nil {
		if err.Error() == "dashboard name already exists" {
			setConflictMsg(ctx)
			return
		} else {
			log.Errorf("ProcessCreateDashboardRequest: could not create dname: %v, id: %v, err=%v", dname, myid, err)
			utils.SetBadMsg(ctx, "")
			return
		}
	}

	utils.WriteJsonResponse(ctx, dashboardInfo)
	ctx.SetStatusCode(fasthttp.StatusOK)
}

func ProcessFavoriteRequest(ctx *fasthttp.RequestCtx) {
	dId := utils.ExtractParamAsString(ctx.UserValue("dashboard-id"))
	if dId == "" {
		log.Errorf("ProcessFavoriteRequest: received empty dashboard id")
		utils.SetBadMsg(ctx, "")
		return
	}

	isFavorite, err := toggleFavorite(dId)
	if err != nil {
		log.Errorf("ProcessFavoriteRequest: could not toggle favorite status for Dashboard=%v, err=%v", dId, err)
		utils.SetBadMsg(ctx, "")
		return
	}

	response := map[string]bool{"isFavorite": isFavorite}
	utils.WriteJsonResponse(ctx, response)
	ctx.SetStatusCode(fasthttp.StatusOK)
}

func ProcessListFavoritesRequest(ctx *fasthttp.RequestCtx, myid uint64) {
	dIds, err := getAllFavoriteDashboardIds(myid)

	if err != nil {
		log.Errorf("ProcessListFavoritesRequest: could not get favorite dashboard ids, err=%v", err)
		utils.SetBadMsg(ctx, "")
		return
	}
	utils.WriteJsonResponse(ctx, dIds)
	ctx.SetStatusCode(fasthttp.StatusOK)
}

func getAllFavoriteDashboardIds(orgId uint64) (map[string]string, error) {
	allDashboards, err := getAllDashboardIds(orgId)
	if err != nil {
		return nil, err
	}

	favoriteDashboards := make(map[string]string)
	for id, name := range allDashboards {
		isFavorite, err := isDashboardFavorite(id)
		if err != nil {
			return nil, err
		}

		if isFavorite {
			favoriteDashboards[id] = name
		}
	}

	return favoriteDashboards, nil
}

func isDashboardFavorite(id string) (bool, error) {
	var dashboardDetailsFname string

	if isDefaultDashboard(id) {
		dashboardDetailsFname = "defaultDBs/details/" + id + ".json"
	} else {
		dashboardDetailsFname = config.GetDataPath() + "querynodes/" + config.GetHostID() + "/dashboards/details/" + id + ".json"
	}

	dashboardJson, err := os.ReadFile(dashboardDetailsFname)
	if err != nil {
		return false, err
	}

	var dashboard map[string]interface{}
	err = json.Unmarshal(dashboardJson, &dashboard)
	if err != nil {
		log.Errorf("isDashboardFavorite: Failed to unmarshal json: %v, err=%v", dashboardJson, err)
		return false, err
	}

	isFav, ok := dashboard["isFavorite"].(bool)
	if !ok {
		isFav = false
	}

	return isFav, nil
}

func ProcessListAllRequest(ctx *fasthttp.RequestCtx, myid uint64) {
	dIds, err := getAllDashboardIds(myid)

	if err != nil {
		log.Errorf("ProcessListAllRequest: could not get dashboard ids, err=%v", err)
		utils.SetBadMsg(ctx, "")
		return
	}
	utils.WriteJsonResponse(ctx, dIds)
	ctx.SetStatusCode(fasthttp.StatusOK)
}

func ProcessListAllDefaultDBRequest(ctx *fasthttp.RequestCtx, myid uint64) {
	dIds, err := getAllDefaultDashboardIds(myid)

	if err != nil {
		log.Errorf("ProcessListAllDefaultDBRequest: could not get dashboard ids, err=%v", err)
		utils.SetBadMsg(ctx, "")
		return
	}
	utils.WriteJsonResponse(ctx, dIds)
	ctx.SetStatusCode(fasthttp.StatusOK)
}

func checkAndReturnFieldInMapIfExists(mapData map[string]interface{}, fieldName string) (interface{}, error) {
	value, exists := mapData[fieldName]
	if !exists {
		return nil, errors.New(fieldName + " field not found")
	}
	return value, nil
}

func parseUpdateDashboardRequest(readJSON map[string]interface{}) (string, string, map[string]interface{}, error) {

	value, err := checkAndReturnFieldInMapIfExists(readJSON, "id")
	if err != nil {
		return "", "", nil, err
	}

	dId, ok := value.(string)
	if !ok {
		return "", "", nil, errors.New("id field is not a string")
	}

	value, err = checkAndReturnFieldInMapIfExists(readJSON, "name")
	if err != nil {
		return "", "", nil, err
	}
	dName, ok := value.(string)
	if !ok {
		return "", "", nil, errors.New("name field is not a string")
	}

	value, err = checkAndReturnFieldInMapIfExists(readJSON, "details")
	if err != nil {
		return "", "", nil, err
	}
	dashboardDetails, ok := value.(map[string]interface{})
	if !ok {
		return "", "", nil, errors.New("details field is not a map")
	}
	return dId, dName, dashboardDetails, nil
}

func ProcessUpdateDashboardRequest(ctx *fasthttp.RequestCtx, myid uint64) {
	rawJSON := ctx.PostBody()
	if rawJSON == nil {
		log.Errorf("ProcessCreateDashboardRequest: received empty user query")
		utils.SetBadMsg(ctx, "")
		return
	}

	readJSON := make(map[string]interface{})

	err := json.Unmarshal(rawJSON, &readJSON)
	if err != nil {
		log.Errorf("ProcessCreateDashboardRequest: could not unmarshall body: %v, err=%v", rawJSON, err)
		utils.SetBadMsg(ctx, "")
		return
	}

	dId, dName, dashboardDetails, err := parseUpdateDashboardRequest(readJSON)
	if err != nil {
		log.Errorf("ProcessCreateDashboardRequest: parseUpdateDashboardRequest failed, readJSON: %v, err: %v", readJSON, err)
		utils.SetBadMsg(ctx, "")
		return
	}
	err = updateDashboard(dId, dName, dashboardDetails, myid)
	if err != nil {
		if err.Error() == "dashboard name already exists" {
			setConflictMsg(ctx)
			return
		} else {
			log.Errorf("ProcessCreateDashboardRequest: could not create Dashboard, dId: %v, myid: %v, err: %v", dId, myid, err)
			utils.SetBadMsg(ctx, "")
			return
		}
	}
	response := "Dashboard updated successfully"
	utils.WriteJsonResponse(ctx, response)
	ctx.SetStatusCode(fasthttp.StatusOK)
}

func ProcessGetDashboardRequest(ctx *fasthttp.RequestCtx) {
	dId := utils.ExtractParamAsString(ctx.UserValue("dashboard-id"))
	dashboardDetails, err := getDashboard(dId)
	if err != nil {
		log.Errorf("ProcessGetDashboardRequest: could not get Dashboard, id: %v, err: %v", dId, err)
		utils.SetBadMsg(ctx, "")
		return
	}
	utils.WriteJsonResponse(ctx, dashboardDetails)
	ctx.SetStatusCode(fasthttp.StatusOK)
}

func ProcessDeleteDashboardRequest(ctx *fasthttp.RequestCtx, myid uint64) {
	dId := utils.ExtractParamAsString(ctx.UserValue("dashboard-id"))
	err := deleteDashboard(dId, myid)
	if err != nil {
		log.Errorf("ProcessDeleteDashboardRequest: Failed to delete dashboard, id: %v, err=%v", dId, err)
		utils.SetBadMsg(ctx, "")
		return
	}

	log.Infof("ProcessDeleteDashboardRequest: Successfully deleted dashboard id: %v", dId)
	err = blob.UploadQueryNodeDir()
	if err != nil {
		log.Errorf("ProcessDeleteDashboardRequest: Failed to upload query nodes dir  err=%v", err)
		return
	}
	response := "Dashboard deleted successfully"
	utils.WriteJsonResponse(ctx, response)
	ctx.SetStatusCode(fasthttp.StatusOK)
}

func ProcessDeleteDashboardsByOrgId(orgid uint64) error {
	dIds, err := getAllDashboardIds(orgid)
	if err != nil {
		log.Errorf("ProcessDeleteDashboardsByOrgId: Failed to get all dashboard ids err=%v", err)
		return err
	}
	for dId := range dIds {
		err = deleteDashboard(dId, orgid)
		if err != nil {
			log.Errorf("ProcessDeleteDashboardsByOrgId: Failed to delete dashboard, id: %v, err: %v", dId, err)
		}

		log.Infof("ProcessDeleteDashboardsByOrgId: Successfully deleted dashboard %v", dId)
		err = blob.UploadQueryNodeDir()
		if err != nil {
			log.Errorf("ProcessDeleteDashboardsByOrgId: Failed to upload query nodes dir, err=%v", err)
			// Move on to the next dashboard for now
		}
	}

	dashboardAllIdsFilename := config.GetDataPath() + "querynodes/" + config.GetHostname() + "/dashboards/allids-" + fmt.Sprint(orgid) + ".json"

	err = os.Remove(dashboardAllIdsFilename)
	if err != nil {
		log.Warnf("ProcessDeleteDashboardsByOrgId: Failed to delete the dashboard allids file: %v", dashboardAllIdsFilename)
	}
	return nil
}
