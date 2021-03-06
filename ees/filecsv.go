/*
Real-time Online/Offline Charging System (OerS) for Telecom & ISP environments
Copyright (C) ITsysCOM GmbH

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>
*/

package ees

import (
	"encoding/csv"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/cgrates/cgrates/engine"

	"github.com/cgrates/cgrates/config"
	"github.com/cgrates/cgrates/utils"
)

func NewFileCSVee(cgrCfg *config.CGRConfig, cfgIdx int, filterS *engine.FilterS,
	dc utils.MapStorage) (fCsv *FileCSVee, err error) {
	dc[utils.ExportID] = cgrCfg.EEsCfg().Exporters[cfgIdx].ID
	fCsv = &FileCSVee{id: cgrCfg.EEsCfg().Exporters[cfgIdx].ID,
		cgrCfg: cgrCfg, cfgIdx: cfgIdx, filterS: filterS, dc: dc}
	err = fCsv.init()
	return
}

// FileCSVee implements EventExporter interface for .csv files
type FileCSVee struct {
	id        string
	cgrCfg    *config.CGRConfig
	cfgIdx    int // index of config instance within ERsCfg.Readers
	filterS   *engine.FilterS
	file      *os.File
	csvWriter *csv.Writer
	sync.RWMutex
	dc utils.MapStorage
}

// init will create all the necessary dependencies, including opening the file
func (fCsv *FileCSVee) init() (err error) {
	// create the file
	if fCsv.file, err = os.Create(path.Join(fCsv.cgrCfg.EEsCfg().Exporters[fCsv.cfgIdx].ExportPath,
		fCsv.id+utils.Underline+utils.UUIDSha1Prefix()+utils.CSVSuffix)); err != nil {
		return
	}
	fCsv.csvWriter = csv.NewWriter(fCsv.file)
	fCsv.csvWriter.Comma = utils.CSV_SEP
	if len(fCsv.cgrCfg.EEsCfg().Exporters[fCsv.cfgIdx].FieldSep) > 0 {
		fCsv.csvWriter.Comma = rune(fCsv.cgrCfg.EEsCfg().Exporters[fCsv.cfgIdx].FieldSep[0])
	}
	return fCsv.composeHeader()
}

// ID returns the identificator of this exporter
func (fCsv *FileCSVee) ID() string {
	return fCsv.id
}

// OnEvicted implements EventExporter, doing the cleanup before exit
func (fCsv *FileCSVee) OnEvicted(_ string, _ interface{}) {
	// verify if we need to add the trailer
	if err := fCsv.composeTrailer(); err != nil {
		utils.Logger.Warning(fmt.Sprintf("<%s> Exporter with id: <%s> received error: <%s> when composed trailer",
			utils.EventExporterS, fCsv.id, err.Error()))
	}
	fCsv.csvWriter.Flush()
	if err := fCsv.file.Close(); err != nil {
		utils.Logger.Warning(fmt.Sprintf("<%s> Exporter with id: <%s> received error: <%s> when closing the file",
			utils.EventExporterS, fCsv.id, err.Error()))
	}
	return
}

// ExportEvent implements EventExporter
func (fCsv *FileCSVee) ExportEvent(cgrEv *utils.CGREvent) (err error) {
	fCsv.Lock()
	defer fCsv.Unlock()

	fCsv.dc[utils.NumberOfEvents] = fCsv.dc[utils.NumberOfEvents].(int) + 1

	var csvRecord []string
	req := utils.MapStorage{}
	for k, v := range cgrEv.Event {
		req[k] = v
	}
	eeReq := NewEventExporterRequest(req, fCsv.dc, cgrEv.Tenant, fCsv.cgrCfg.GeneralCfg().DefaultTimezone,
		fCsv.filterS)

	if err = eeReq.SetFields(fCsv.cgrCfg.EEsCfg().Exporters[fCsv.cfgIdx].ContentFields()); err != nil {
		fCsv.dc[utils.NegativeExports].(utils.StringSet).Add(cgrEv.ID)
		return
	}
	for el := eeReq.cnt.GetFirstElement(); el != nil; el = el.Next() {
		var strVal string
		if strVal, err = eeReq.cnt.FieldAsString(el.Value.Slice()); err != nil {
			return
		}
		csvRecord = append(csvRecord, strVal)
	}
	if aTime, err := cgrEv.FieldAsTime(utils.AnswerTime, fCsv.cgrCfg.GeneralCfg().DefaultTimezone); err == nil {
		if fCsv.dc[utils.FirstEventATime].(time.Time).IsZero() || fCsv.dc[utils.FirstEventATime].(time.Time).Before(aTime) {
			fCsv.dc[utils.FirstEventATime] = aTime
		}
		if aTime.After(fCsv.dc[utils.LastEventATime].(time.Time)) {
			fCsv.dc[utils.LastEventATime] = aTime
		}
	}
	if oID, err := cgrEv.FieldAsInt64(utils.OrderID); err == nil {
		if fCsv.dc[utils.FirstExpOrderID].(int64) > oID || fCsv.dc[utils.FirstExpOrderID].(int64) == 0 {
			fCsv.dc[utils.FirstExpOrderID] = oID
		}
		if fCsv.dc[utils.LastExpOrderID].(int64) < oID {
			fCsv.dc[utils.LastExpOrderID] = oID
		}
	}
	if cost, err := cgrEv.FieldAsFloat64(utils.Cost); err == nil {
		fCsv.dc[utils.TotalCost] = fCsv.dc[utils.TotalCost].(float64) + cost
	}
	if tor, err := cgrEv.FieldAsString(utils.ToR); err == nil {
		if usage, err := cgrEv.FieldAsDuration(utils.Usage); err == nil {
			switch tor {
			case utils.VOICE:
				fCsv.dc[utils.TotalDuration] = fCsv.dc[utils.TotalDuration].(time.Duration) + usage
			case utils.SMS:
				fCsv.dc[utils.TotalSMSUsage] = fCsv.dc[utils.TotalSMSUsage].(time.Duration) + usage
			case utils.MMS:
				fCsv.dc[utils.TotalMMSUsage] = fCsv.dc[utils.TotalMMSUsage].(time.Duration) + usage
			case utils.GENERIC:
				fCsv.dc[utils.TotalGenericUsage] = fCsv.dc[utils.TotalGenericUsage].(time.Duration) + usage
			case utils.DATA:
				fCsv.dc[utils.TotalDataUsage] = fCsv.dc[utils.TotalDataUsage].(time.Duration) + usage
			}
		}
	}
	fCsv.dc[utils.PositiveExports].(utils.StringSet).Add(cgrEv.ID)
	fCsv.csvWriter.Write(csvRecord)
	return
}

// Compose and cache the header
func (fCsv *FileCSVee) composeHeader() (err error) {
	if len(fCsv.cgrCfg.EEsCfg().Exporters[fCsv.cfgIdx].HeaderFields()) == 0 {
		return
	}
	var csvRecord []string
	eeReq := NewEventExporterRequest(nil, fCsv.dc, fCsv.cgrCfg.GeneralCfg().DefaultTenant, fCsv.cgrCfg.GeneralCfg().DefaultTimezone,
		fCsv.filterS)
	if err = eeReq.SetFields(fCsv.cgrCfg.EEsCfg().Exporters[fCsv.cfgIdx].HeaderFields()); err != nil {
		return
	}
	for el := eeReq.hdr.GetFirstElement(); el != nil; el = el.Next() {
		var strVal string
		if strVal, err = eeReq.hdr.FieldAsString(el.Value.Slice()); err != nil {
			return
		}
		csvRecord = append(csvRecord, strVal)
	}
	return fCsv.csvWriter.Write(csvRecord)
}

// Compose and cache the trailer
func (fCsv *FileCSVee) composeTrailer() (err error) {
	if len(fCsv.cgrCfg.EEsCfg().Exporters[fCsv.cfgIdx].TrailerFields()) == 0 {
		return
	}
	var csvRecord []string
	eeReq := NewEventExporterRequest(nil, fCsv.dc, fCsv.cgrCfg.GeneralCfg().DefaultTenant, fCsv.cgrCfg.GeneralCfg().DefaultTimezone,
		fCsv.filterS)
	if err = eeReq.SetFields(fCsv.cgrCfg.EEsCfg().Exporters[fCsv.cfgIdx].TrailerFields()); err != nil {
		return
	}
	for el := eeReq.trl.GetFirstElement(); el != nil; el = el.Next() {
		var strVal string
		if strVal, err = eeReq.trl.FieldAsString(el.Value.Slice()); err != nil {
			return
		}
		csvRecord = append(csvRecord, strVal)
	}
	return fCsv.csvWriter.Write(csvRecord)
}
