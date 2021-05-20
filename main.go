package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	dialogflow "cloud.google.com/go/dialogflow/apiv2"
	structpb "github.com/golang/protobuf/ptypes/struct"
	"google.golang.org/api/option"
	dialogflowpb "google.golang.org/genproto/googleapis/cloud/dialogflow/v2"
)

// DialogflowProcessor has all the information for connecting with Dialogflow
type DialogflowProcessor struct {
	projectID        string
	authJSONFilePath string
	lang             string
	timeZone         string
	sessionClient    *dialogflow.SessionsClient
	ctx              context.Context
}

// NLPResponse is the struct for the response
type NLPResponse struct {
	Intent     string            `json:"intent"`
	Text       string            `json:"text"`
	Confidence float32           `json:"confidence"`
	Entities   map[string]string `json:"entities"`
}

// Example response object
/*
2021/05/18 14:23:29
query_text:"Coffee"
language_code:"en"
action:"input.unknown"
parameters:{}
all_required_params_present:true
fulfillment_text:"Default: I didn't get that. Can you say it again?"
fulfillment_messages:{text:{text:"Default: I didn't get that. Can you say it again?"}}
output_contexts:{
	name:"projects/buoyant-cargo-314008/agent/sessions/testUser/contexts/__system_counters__"
	lifespan_count:1
	parameters:{
		fields:{
			key:"no-input"
			value:{number_value:0}
		}
		fields:{
			key:"no-match"
			value:{number_value:1}
		}
	}
}
intent:{
	name:"projects/buoyant-cargo-314008/agent/intents/1f3b775b-81a1-4cee-832a-58dac8f75b90"
	display_name:"Default Fallback Intent"
	is_fallback:true
}
intent_detection_confidence:1
main.NLPResponse{
	Intent:"Default Fallback Intent",
	Confidence:1,
	Entities:map[string]string{}
}
*/

var dp DialogflowProcessor

func main() {
	dp.init("buoyant-cargo-314008", "bot-hackaton21-key.json", "en", "America/Montevideo")
	http.HandleFunc("/", requestHandler)
	go http.ListenAndServe(":5000", nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		fmt.Println("Coffee bot")
		fmt.Println("---------------------")
		defer wg.Done()

		for {
			fmt.Print("-> ")
			text, _ := reader.ReadString('\n')
			// convert CRLF to LF
			text = strings.Replace(text, "\n", "", -1)
			values := map[string]string{"Message": text}
			json_data, err := json.Marshal(values)

			if err != nil {
				log.Fatal(err)
			}
			http.Post("http://localhost:5000", "application/json", bytes.NewBuffer(json_data))

			if strings.Compare("exit", text) == 0 {
				fmt.Println("Exiting")
				wg.Done()
			}
		}
	}()
	wg.Wait()
}

func requestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		//POST method, receives a json to parse
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading request body",
				http.StatusInternalServerError)
		}
		type inboundMessage struct {
			Message string
		}
		var m inboundMessage
		err = json.Unmarshal(body, &m)
		if err != nil {
			panic(err)
		}

		// Use NLP
		response := dp.processNLP(m.Message, "testUser")
		fmt.Printf("%#v\n", response.Text)
		w.Header().Set("Content-Type", "application/json")
		//json.NewEncoder(w).Encode(response)
		json.NewEncoder(w).Encode(response)
	}
}

func (dp *DialogflowProcessor) init(a ...string) (err error) {
	dp.projectID = a[0]
	dp.authJSONFilePath = a[1]
	dp.lang = a[2]
	dp.timeZone = a[3]

	// Auth process: https://dialogflow.com/docs/reference/v2-auth-setup

	dp.ctx = context.Background()
	sessionClient, err := dialogflow.NewSessionsClient(dp.ctx, option.WithCredentialsFile(dp.authJSONFilePath))
	if err != nil {
		log.Fatal("Error in auth with Dialogflow")
	}
	dp.sessionClient = sessionClient

	return
}

func (dp *DialogflowProcessor) processNLP(rawMessage string, username string) (r NLPResponse) {
	sessionID := username
	request := dialogflowpb.DetectIntentRequest{
		Session: fmt.Sprintf("projects/%s/agent/sessions/%s", dp.projectID, sessionID),
		QueryInput: &dialogflowpb.QueryInput{
			Input: &dialogflowpb.QueryInput_Text{
				Text: &dialogflowpb.TextInput{
					Text:         rawMessage,
					LanguageCode: dp.lang,
				},
			},
		},
		QueryParams: &dialogflowpb.QueryParameters{
			TimeZone: dp.timeZone,
		},
	}
	response, err := dp.sessionClient.DetectIntent(dp.ctx, &request)
	if err != nil {
		log.Fatalf("Error in communication with Dialogflow %s", err.Error())
		return
	}
	queryResult := response.GetQueryResult()
	if queryResult.Intent != nil {
		r.Text = queryResult.FulfillmentText
		r.Intent = queryResult.Intent.DisplayName
		r.Confidence = float32(queryResult.IntentDetectionConfidence)
	}
	r.Entities = make(map[string]string)
	params := queryResult.Parameters.GetFields()
	if len(params) > 0 {
		for paramName, p := range params {
			// fmt.Printf("Param %s: %s (%s)\n", paramName, p.GetStringValue(), p.String())
			extractedValue := extractDialogflowEntities(p)
			r.Entities[paramName] = extractedValue
		}
	}
	return
}

func extractDialogflowEntities(p *structpb.Value) (extractedEntity string) {
	kind := p.GetKind()
	switch kind.(type) {
	case *structpb.Value_StringValue:
		return p.GetStringValue()
	case *structpb.Value_NumberValue:
		return strconv.FormatFloat(p.GetNumberValue(), 'f', 6, 64)
	case *structpb.Value_BoolValue:
		return strconv.FormatBool(p.GetBoolValue())
	case *structpb.Value_StructValue:
		s := p.GetStructValue()
		fields := s.GetFields()
		extractedEntity = ""
		for key, value := range fields {
			if key == "amount" {
				extractedEntity = fmt.Sprintf("%s%s\n", extractedEntity, strconv.FormatFloat(value.GetNumberValue(), 'f', 6, 64))
			}
			if key == "unit" {
				extractedEntity = fmt.Sprintf("%s%s\n", extractedEntity, value.GetStringValue())
			}
			if key == "date_time" {
				extractedEntity = fmt.Sprintf("%s%s\n", extractedEntity, value.GetStringValue())
			}
			// @TODO: Other entity types can be added here
		}
		return extractedEntity
	case *structpb.Value_ListValue:
		list := p.GetListValue()
		for _, val := range list.GetValues() {
			extractedEntity = extractDialogflowEntities(val)
		}
		return extractedEntity
	default:
		return ""
	}
}
