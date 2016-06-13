package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TeamFairmont/boltsdk-go/boltsdk"
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

	boltsdk.EnableLogOutput(true)

	prefix := "SearchExample::"
	boltsdk.RunWorker(mq, prefix, "getBaseResults", getBaseResults)
	boltsdk.RunWorker(mq, prefix, "addQuickestMetaInfo", addQuickestMetaInfo)
	boltsdk.RunWorker(mq, prefix, "fetchPage", fetchPage)
	boltsdk.RunWorker(mq, prefix, "saveToIndex", saveToIndex)
	boltsdk.RunWorker(mq, prefix, "parseKeywords", parseKeywords)

	log.Println("Worker setup complete, waiting for commands... ")

	//let our goroutine above loop forever or until Ctrl+C
	forever := make(chan bool)
	<-forever
}

/******************
INDEX RELATED FUNCS
******************/
func fetchPage(payload *gabs.Container) error {
	//request the http url
	url := payload.Path("initial_input.url").Data().(string)
	payload.SetP(url, "return_value.url")

	//Example of forcing a halting condition and returning an error up to the bolt engine and api client
	//payload.SetP(boltsdk.HaltCallCommandName, "nextCommand")
	//payload.SetP("FAKE ERROR", "error")
	//return errors.New("FAKE ERROR")

	resp, err := http.Get(url)
	if err != nil {
		payload.SetP("Error getting url content: "+err.Error(), "error.fetchPage")
		return errors.New("Error getting url content: " + err.Error())
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		payload.SetP("Error getting url content: "+err.Error(), "error.fetchPage")
		return errors.New("Error getting url content: " + err.Error())
	}
	payload.SetP(string(body), "return_value.html")

	return nil
}

func parseKeywords(payload *gabs.Container) error {
	//parse through html to extract unique 5+ letter a-zA-Z keywords
	html := ""
	if payload.Path("return_value.html").Data() != nil {
		html = payload.Path("return_value.html").Data().(string)
	}

	//super inefficient way to do this, a full app would parse the html tree
	//properly, treat certain elements differently, etc
	tmptokens := strings.Split(html, " ")

	//easy way to de-dupe keywords we'll be filling this map using the keys as the keyword
	//could also use it to up the # of occurences of the keyword to add weights
	keywords := make(map[string]int)
	for i := range tmptokens {
		//here we make sure to only include tokens that are alpha w/ no numerics or special chars
		matched, err := regexp.MatchString("^[a-zA-Z]+$", tmptokens[i])
		if err == nil && len(tmptokens[i]) >= 5 && matched {
			keywords[strings.ToLower(tmptokens[i])] = 1
		}
	}

	//fill a slice with the unique keywords
	kwarray := []string{}
	for kw := range keywords {
		kwarray = append(kwarray, kw)
	}

	//cheapo extract title of page without full html parse
	title := "(Unknown)"
	i := strings.Index(html, "<title>")
	e := strings.Index(html, "</title>")
	if i >= 0 && e >= 0 && i < e {
		title = html[i+7 : e]
	}

	//add to the payload
	payload.SetP(kwarray, "return_value.keywords")
	payload.SetP(title, "return_value.title")
	payload.SetP("", "return_value.html") //clear out the html, no need to send it to next workers

	return nil
}

func saveToIndex(payload *gabs.Container) error {
	//loop through keywords, adding the url to keyword maps
	//(in a real application, this would be some sort of real datastore, of course)
	kw := payload.Path("return_value.keywords").Data().([]interface{})
	url := payload.Path("return_value.url").Data().(string)
	title := payload.Path("return_value.title").Data().(string)

	//fmt.Println(kw, url, title)
	sites[url] = &ResultInfo{Title: title, URL: url, Meta: ""}

	//with multiple worker threads running, this might cause locking and need a mutex, but for example purpose its ok.
	//we look through all keywords, and store the association to the url
	for keywordi := range kw {
		keyword := kw[keywordi].(string)
		urls, ok := keywords[keyword]
		if ok {
			skip := false
			for u := range urls.C {
				if url == urls.C[u] { //site already in this keyword assoc
					skip = true
				}
			}
			if !skip {
				urls.C = append(urls.C, url)
			}
		} else {
			keywords[keyword] = &URLCollection{C: []string{url}}
		}
	}
	return nil
}

/******************
SEARCH RELATED FUNCS
******************/
func getBaseResults(payload *gabs.Container) error {
	//do some work/db queries, etc (in this case, 'query' the search index)
	stopAt := int(payload.Path("params.stopAt").Data().(float64))
	results := doSearch(payload.Path("initial_input.searchtext").Data().(string), stopAt)
	_, err := payload.SetP(results, "return_value.results")
	return err
}

func doSearch(search string, stopAt int) []*ResultInfo {
	search = strings.ToLower(search)
	terms := strings.Split(search, " ")
	//TODO de-dupe terms!
	tempres := make(map[string]*ResultInfo)
	results := []*ResultInfo{}

	//naive keyword match and score storage
OuterLoop:
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

					//HACK: if we get to 4 results, purposefully slow down the results for demo purposes
					if len(tempres) == 4 {
						r := rand.Intn(4000) + 1000
						time.Sleep(time.Duration(r) * time.Millisecond)
					}

					//stop "assembling results" at stopAt
					if len(tempres) >= stopAt {
						break OuterLoop
					}
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

func addQuickestMetaInfo(payload *gabs.Container) error {
	//add some 'fake' third party vendor meta lookups for the search results
	_ = simulateThirdPartyMeta()
	return nil
}

func simulateThirdPartyMeta() string {
	r := rand.Intn(1000)
	time.Sleep(time.Duration(r) * time.Millisecond)
	return strconv.Itoa(r)
}
