package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/TeamFairmont/boltshared/mqwrapper"
	"github.com/TeamFairmont/gabs"
)

// URLCollection ...
type URLCollection struct {
	C []string
}

// ResultInfo ...
type ResultInfo struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Meta  string `json:"meta"`
	Score int    `json:"score"`
}

var keywords map[string]*URLCollection
var sites map[string]*ResultInfo

func main() {
	keywords = make(map[string]*URLCollection)
	sites = make(map[string]*ResultInfo)

	//preload some fake data in there
	keywords["vanilla"] = &URLCollection{C: []string{"http://google.com", "http://yahoo.com"}}
	keywords["chocolate"] = &URLCollection{C: []string{"http://google.com", "http://bing.com"}}
	keywords["awesome"] = &URLCollection{C: []string{"http://commercev3.com"}}

	sites["http://google.com"] = &ResultInfo{Title: "Google", URL: "http://google.com", Meta: "Google is a popular search engine"}
	sites["http://bing.com"] = &ResultInfo{Title: "Bing", URL: "http://bing.com", Meta: "Bing is a somewhat popular search engine"}
	sites["http://yahoo.com"] = &ResultInfo{Title: "Yahoo", URL: "http://yahoo.com", Meta: "Yahoo is a not so popular search engine"}
	sites["http://commercev3.com"] = &ResultInfo{Title: "CommerceV3", URL: "http://commercev3.com", Meta: "CV3 is awesome!"}

	//connect to mq server
	mq, err := mqwrapper.ConnectMQ("amqp://guest:guest@localhost:5672/")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	getBaseResults(mq)
	addQuickestMetaInfo(mq)
	//TODO add to index functionality

	log.Println("Worker waiting for commands... ")

	//let our goroutine above loop forever or until Ctrl+C
	forever := make(chan bool)
	<-forever
}

func getBaseResults(mq *mqwrapper.Connection) {
	//set the name of our command
	name := "getBaseResults"

	//set base QoS parms
	ch, _ := mq.Connection.Channel()
	ch.Qos(
		1,     // prefetch count
		0,     // prefetch size
		false, // global
	)

	//connect to the proper mq consumer for this command
	q, res, err := mqwrapper.CreateConsumeNamedQueue(name, ch)
	_ = q // don't need the q for this example, but might need it in more complex examples
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	} else {
		// spin up the goroutine to process work
		go func() {
			for d := range res {
				log.Println("getBaseResults in")

				//grab the message body and parse to json obj
				payload, err := gabs.ParseJSON(d.Body)
				if err != nil {
					fmt.Println(err)
				}

				//do some work/db queries, etc (in this case, 'query' the search index)
				results := doSearch(payload.Path("initial_input.searchtext").Data().(string))
				_, err = payload.SetP(results, "return_value.results")

				// push our response to the temp mq replyTo path
				err = mqwrapper.PublishCommand(ch, d.CorrelationId, d.ReplyTo, payload, "")
				if err != nil {
					log.Println(err)
				}

				d.Ack(false) //tell mq we've handled the message
				log.Println("getBaseResults out")
			}
		}()
	}
}

func doSearch(search string) []*ResultInfo {
	search = strings.ToLower(search)
	terms := strings.Split(search, " ")
	//TODO de-dupe terms!
	tempres := make(map[string]*ResultInfo)
	results := []*ResultInfo{}

	//naive keyword match and score storage
	for i := range terms {
		if len(terms[i]) >= 3 {
			c, kwok := keywords[terms[i]]
			if kwok {
				for u := range c.C {
					_, ok := tempres[c.C[u]]
					if !ok {
						s := *sites[c.C[u]]
						tempres[c.C[u]] = &s
					}
					tempres[c.C[u]].Score++
				}
			}
		}
	}

	for _, v := range tempres {
		results = append(results, v)
	}
	//TODO sort by score!

	return results
}

func addQuickestMetaInfo(mq *mqwrapper.Connection) {
	//set the name of our command
	name := "addQuickestMetaInfo"

	//set base QoS parms
	ch, _ := mq.Connection.Channel()
	ch.Qos(
		1,     // prefetch count
		0,     // prefetch size
		false, // global
	)

	//connect to the proper mq consumer for this command
	q, res, err := mqwrapper.CreateConsumeNamedQueue(name, ch)
	_ = q // don't need the q for this example, but might need it in more complex examples
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	} else {
		// spin up the goroutine to process work
		go func() {
			for d := range res {
				log.Println("addQuickestMetaInfo in")

				//grab the message body and parse to json obj
				payload, err := gabs.ParseJSON(d.Body)
				if err != nil {
					fmt.Println(err)
				}

				//TODO do some 'fake' third party vendor meta lookups for the search results

				// push our response to the temp mq replyTo path
				err = mqwrapper.PublishCommand(ch, d.CorrelationId, d.ReplyTo, payload, "")
				if err != nil {
					log.Println(err)
				}

				d.Ack(false) //tell mq we've handled the message
				log.Println("addQuickestMetaInfo out")
			}
		}()

	}
}
