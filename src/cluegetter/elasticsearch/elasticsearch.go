// ClueGetter - Does things with mail
//
// Copyright 2016 Dolf Schimmel, Freeaqingme.
//
// This Source Code Form is subject to the terms of the two-clause BSD license.
// For its contents, please refer to the LICENSE file.
//
package elasticsearch

import (
	"encoding/hex"
	"encoding/json"
	"time"

	"cluegetter/address"
	"cluegetter/core"

	"gopkg.in/olivere/elastic.v3"
	"strings"
)

const ModuleName = "elasticsearch"

// Needs updating with every significant mapping update
const mappingVersion = "1"

type module struct {
	*core.BaseModule

	esClient *elastic.Client
}

type session struct {
	*core.MilterSession
}

func init() {
	core.ModuleRegister(&module{
		BaseModule: core.NewBaseModule(nil),
	})
}

func (m *module) Name() string {
	return ModuleName
}

func (m *module) Enable() bool {
	return m.Config().Elasticsearch.Enabled
}

func (m *module) Init() {
	var err error
	m.esClient, err = elastic.NewClient(
		elastic.SetSniff(m.Config().Elasticsearch.Sniff),
		elastic.SetURL(m.Config().Elasticsearch.Url...),
	)
	if err != nil {
		m.Log().Fatalf("Could not connect to ElasticSearch: %s", err.Error())
	}

	template := `{
  "template": "cluegetter-*-%%MAPPING_VERSION%%",
  "settings": {
    "number_of_shards": 5
  },
  "aliases" : {
    "cluegetter-sessions" : {}
  },
  "settings":{
    "analysis": {
      "analyzer": {
        "lowercase": {
          "type": "custom",
          "tokenizer": "keyword",
          "filter": [
            "lowercase"
          ]
        }
      }
    }
  },
  "mappings": {
    "session": {
      "_all": {
        "enabled": false
      },
      "properties": {
        "InstanceId":     { "type": "integer" },
        "DateConnect":    { "type": "date"    },
        "DateDisconnect": { "type": "date"    },
        "SaslUsername":   {
          "type":     "string",
          "analyzer": "lowercase"
        },
        "SaslSender":     {
          "type":     "string",
          "analyzer": "lowercase"
        },
        "SaslMethod":     {
          "type":  "string",
          "index": "not_analyzed"
        },
        "CertIssuer":     { "type": "string"  },
        "CipherBits":     { "type": "short"   },
        "Cipher":         { "type": "string"  },
        "TlsVersion":     {
          "type":  "string",
          "index": "not_analyzed"
        },
        "Ip":             {
          "type":     "string",
          "analyzer": "lowercase"
        },
        "ReverseDns":     { "type": "string"  },
        "Hostname":       { "type": "string"  },
        "Helo":           { "type": "string"  },
        "MtaHostName":    { "type": "string"  },
        "MtaDaemonName":  { "type": "string"  },

        "Messages": {
          "type": "nested",
          "properties": {
            "QueueId": {
              "type":  "string",
              "index": "not_analyzed"
            },
            "From": {
              "properties": {
                "Local": {
                  "type":     "string",
                  "analyzer": "lowercase"
                },
                "Domain": {
                  "type":     "string",
                  "analyzer": "lowercase"
                }
              }
            },
            "Rcpt": {
              "type": "nested",
              "properties": {
                "Local":  {
                  "type":     "string",
                  "analyzer": "lowercase"
                },
                "Domain": {
                  "type":     "string",
                  "analyzer": "lowercase"
                }
              }
            },
            "Headers": {
              "type": "nested",
              "properties": {
                "Key":   { "type": "string" },
                "Value": { "type": "string" }
              }
            },

            "Date":                   { "type": "date"    },
            "BodySize":               { "type": "integer" },
            "BodyHash":               { "type": "string"  },
            "Verdict":                { "type": "integer" },
            "VerdictMsg":             {
              "type":     "string",
              "analyzer": "simple"
            },
            "RejectScore":            { "type": "float"   },
            "RejectScoreThreshold":   { "type": "float"   },
            "TempfailScore":          { "type": "float"   },
            "TempfailScoreThreshold": { "type": "float"   },

            "CheckResults": {
              "type": "nested",
              "properties": {
                "Module":         {
                  "type":  "string",
                  "index": "not_analyzed"
                },
                "Verdict":        { "type": "integer" },
                "Message":        { "type": "string" },
                "Score":          { "type": "float" },
                "WeightedScore":  { "type": "float" },
                "Duration":       { "type": "long" },
                "Determinants":   { "type": "string" }
              }
            }

          }
        }
      }
    }
  }
}
	`

	template = strings.Replace(template, "%%MAPPING_VERSION%%", mappingVersion, -1)

	_, err = m.esClient.IndexPutTemplate("cluegetter-" + mappingVersion).BodyString(template).Do()
	if err != nil {
		m.Log().Fatalf("Could not create ES template: %s", err.Error())
	}
}

func (m *module) SessionDisconnect(sess *core.MilterSession) {
	m.persistSession(sess)
}

// TODO: Check what happens if we added a message-id header ourselves
func (m *module) persistSession(coreSess *core.MilterSession) {
	if coreSess.ClientIsMonitorHost() && len(coreSess.Messages) == 0 {
		return
	}

	sess := &session{coreSess}

	str, _ := sess.esMarshalJSON(m)
	id := hex.EncodeToString(sess.Id())

	_, err := m.esClient.Index().
		Index("cluegetter-" + sess.DateConnect.Format("20060102") + "-" + mappingVersion).
		Type("session").
		Id(id).
		BodyString(string(str)).
		Do()

	if err != nil {
		m.Log().Errorf("Could not index session '%s', error: %s", id, err.Error())
		return
	}
	//fmt.Printf("Indexed tweet %s to index %s, type %s\n", put1.Id, put1.Index, put1.Type)

}

func (s *session) esMarshalJSON(m *module) ([]byte, error) {
	type Alias session

	esMessages := []*esMessage{}
	for _, v := range s.Messages {
		esMessages = append(esMessages, &esMessage{v})
	}

	out := &struct {
		InstanceId uint
		*Alias
		EsMessages []*esMessage `json:"Messages"`
	}{
		InstanceId: m.Instance(),
		Alias:      (*Alias)(s),
		EsMessages: esMessages,
	}

	return json.Marshal(out)
}

type esMessage struct {
	*core.Message
}

func (m *esMessage) MarshalJSON() ([]byte, error) {
	type Alias esMessage

	out := &struct {
		*Alias
	}{
		Alias: (*Alias)(m),
	}

	return json.Marshal(out)
}

func (s *session) UnmarshalJSON(data []byte) error {
	type Alias session

	aux := &struct {
		*Alias
		InstanceId uint
		Messages   []esMessage
	}{
		Alias:    (*Alias)(s),
		Messages: make([]esMessage, 0),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	aux.Alias.Messages = make([]*core.Message, 0)
	for _, msg := range aux.Messages {
		aux.Alias.Messages = append(aux.Alias.Messages, (*core.Message)(msg.Message))
	}

	s.Instance = aux.InstanceId
	return nil
}

func (m *esMessage) UnmarshalJSON(data []byte) error {
	type Alias esMessage

	aux := &struct {
		*Alias
		From struct {
			Local  string
			Domain string
		}
		Rcpt []struct {
			Local  string
			Domain string
		}
		CheckResults []struct {
			Module          string
			SuggestedAction int
			Message         string
			Score           float64
			Determinants    string
			Duration        time.Duration
			WeightedScore   float64
		}
	}{
		Alias: (*Alias)(m),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	aux.Alias.From = address.FromString(aux.From.Local + "@" + aux.From.Domain)
	for _, v := range aux.Rcpt {
		aux.Alias.Rcpt = append(aux.Alias.Rcpt, address.FromString(v.Local+"@"+v.Domain))
	}
	for _, v := range aux.CheckResults {
		var determinants interface{}
		determinantsMap := make(map[string]interface{}, 0)
		var err error
		if err = json.Unmarshal([]byte(v.Determinants), &determinants); err != nil {
			determinantsMap["error"] = "Could not unmarshal determinants from Elasticsearch Database: " + err.Error()
		} else if determinants == nil {
			determinantsMap = make(map[string]interface{}, 0)
		} else {
			determinantsMap = determinants.(map[string]interface{})
		}

		aux.Alias.CheckResults = append(aux.Alias.CheckResults, &core.MessageCheckResult{
			Module:          v.Module,
			SuggestedAction: v.SuggestedAction,
			Score:           v.Score,
			Duration:        v.Duration,
			WeightedScore:   v.WeightedScore,
			Determinants:    determinantsMap,
		})
	}

	return nil
}
