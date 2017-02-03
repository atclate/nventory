package nvclient

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os/user"
	"regexp"
	"strings"

	"github.com/lestrrat/go-libxml2"
	"github.com/lestrrat/go-libxml2/clib"
	"github.com/lestrrat/go-libxml2/types"
)

type Client interface {
	GetObjects(objecttypes string, conditions map[string][]string, includes map[string][]string, login string, passwordCallback func() string) (Result, error)
	SetObjects(objecttypes string, conditions map[string][]string, includes map[string][]string, set map[string]string, login string, passwordCallback func() string) (string, error)
}

func NewNvClient(httpClient *http.Client, host string, passwordCallback func() string, log *Log) Client {
	client := &NvClient{
		server:           host,
		httpClient:       httpClient,
		PasswordCallback: passwordCallback,
	}
	client.httpClient = httpClient
	client.logger = log
	return client
}

type NvClient struct {
	httpClient       *http.Client
	server           string
	PasswordCallback func() string
	logger           *Log

	Input *bufio.Reader

	redirflag      bool
	numRedirects   int
	subsystemNames []string
}

func (n *NvClient) addClient(client *http.Client) {
	n.httpClient = client
}

func (n *NvClient) getClient() *http.Client {
	return n.httpClient
}

func (d *NvClient) GetServer() string {
	return d.server
}

func (f *NvClient) GetObjects(object_type string, conditions map[string][]string, includes map[string][]string, login string, passwordCallback func() string) (Result, error) {
	i, err := f.getAllSubsystemNames(object_type)
	if err != nil {
		return nil, err
	}
	includes["include"] = intersection(i, includes["include"])

	u := f.getSearchUrl(object_type, conditions, includes)
	f.logger.Debug.Println(fmt.Sprintf("URL: %v", u))

	resp, _ := f.httpClient.Get(u)

	responseStr, err := readResponseBody(resp.Body)
	if err != nil {
		log.Fatal("Unable to read response body.")
	}

	res, err := f.getFieldValue(responseStr)
	return res, err
}

func (f *NvClient) SetObjects(object_type string, conditions map[string][]string, includes map[string][]string, set map[string]string, login string, passwordCallback func() string) (string, error) {
	_, err := f.getAllSubsystemNames(object_type)
	if err != nil {
		return "Unable to get all subsystem names.", err
	}

	u := f.getSearchUrl(object_type, conditions, includes)
	f.logger.Debug.Println(fmt.Sprintf("Search URL: %v", u))

	resp, _ := f.httpClient.Get(u)

	responseStr, err := readResponseBody(resp.Body)
	if err != nil {
		log.Fatal("Unable to read response body.")
	}

	res, err := f.getResultsFromResponse(responseStr)

	numSuccess := 0

	switch t := res.(type) {
	case *ResultArray:
		if len(t.Array) > 0 {
			con := PromptUserConfirmation(fmt.Sprintf("This will update %v entry, continue?  [y/N]: ", len(t.Array)), f.Input)
			if con {
				for _, item := range t.Array {
					switch t2 := item.(type) {
					case *ResultMap:
						idVal := t2.Get("id")
						if idVal == nil {

						} else if id, ok := idVal.(*ResultValue); ok && id.Value != "" {
							f.logger.Debug.Printf("Set: %v", set)
							values := url.Values{}
							for k, v := range set {
								re, err := regexp.Compile(`[.+]`)
								if err == nil && re.Match([]byte(k)) {
									values.Set(k, v)
								} else {
									values.Set(t2.ID()+"["+k+"]", v)
								}
							}

							u := f.getSetUrl(object_type, id.Value, values.Encode())
							f.logger.Debug.Printf("Set URL: %v\n", u)

							req, err := http.NewRequest("PUT", u, nil)
							if err != nil {
								fmt.Println("Error creating PUT request for url: " + u)
							} else {
								f.logger.Debug.Printf("PUT Request: %v", req)
								if u, err := user.Current(); err == nil {
									f.httpClient, err = GetHttpClientFor(f.GetServer(), u.Username, passwordCallback)
								}
								isRedirect := true
								err = nil
								for isRedirect && err == nil {
									req, _ = http.NewRequest(req.Method, req.URL.String(), nil)
									resp, err = f.httpClient.Do(req)
									//									f.logger.Debug.Printf("Response from %v:\n%v\n", req.URL.String(), resp)
									isRedirect = isRedirectResponse(resp)
									if isRedirect {
										f.logger.Debug.Printf("Redirecting to %v from %v\n", getHeaderLocation(resp), req.URL.String())
										u, err := url.Parse(getHeaderLocation(resp))
										if err == nil {
											req.URL = u
										}
									}
								}

								if err != nil {
									fmt.Printf("Error requesting PUT request for url: %v\nError: %v\n", u, err)
								} else {
									body, err := readResponseBody(resp.Body)
									if err == nil {
										f.logger.Debug.Printf("Success Response Body:\n%v\n", body)
										numSuccess++
									} else {
										fmt.Printf("Error: %v", err)
									}
								}
							}
						}
					}
				}
			} else {
				return fmt.Sprintln("Cancelled"), nil
			}
			msg := fmt.Sprintf("%v out of %v update(s) succeeded.\n", numSuccess, len(t.Array))
			if numSuccess != len(t.Array) {
				err = errors.New(fmt.Sprintf("%v out of %v update(s) failed.\n", len(t.Array)-numSuccess, len(t.Array)))
			}
			return msg, err
		}
	}

	name := ""
	if conditions[""] != nil {
		name = conditions[""][0]
	}
	if set["name"] == "" || len(set["name"]) == 0 {
		set["name"] = name
	} else {
		name = set["name"]
	}

	con := PromptUserConfirmation(fmt.Sprintf("This will create new entry (%v), continue?  [y/N]: ", name), f.Input)
	if con {
		f.logger.Debug.Printf("Set: %v", set)
		values := url.Values{}
		for k, v := range set {
			re, err := regexp.Compile(`[.+]`)
			if err == nil && re.Match([]byte(k)) {
				values.Set(k, v)
			} else {
				values.Set(singularize(object_type)+"["+k+"]", v)
			}
		}

		u := f.getCreateUrl(object_type, values.Encode())
		f.logger.Debug.Printf("Create URL: %v\n", u)

		req, err := http.NewRequest("POST", u, nil)
		if err != nil {
			fmt.Println("Error creating POST request for url: " + u)
		} else {
			f.logger.Debug.Printf("POST Request: %v", req)
			if u, err := user.Current(); err == nil {
				f.httpClient, err = GetHttpClientFor(f.GetServer(), u.Username, passwordCallback)
			}
			isRedirect := true
			err = nil
			for isRedirect && err == nil {
				req, _ = http.NewRequest(req.Method, req.URL.String(), nil)
				resp, err = f.httpClient.Do(req)
				f.logger.Debug.Printf("Response from %v:\n%v\n", req.URL.String(), resp)
				isRedirect = isRedirectResponse(resp)
				if isRedirect {
					f.logger.Debug.Printf("Redirecting to %v from %v\n", getHeaderLocation(resp), req.URL.String())
					u, err := url.Parse(getHeaderLocation(resp))
					if err == nil {
						req.URL = u
					}
				}
			}

			if err != nil {
				fmt.Printf("Error requesting PUT request for url: %v\nError: %v\n", u, err)
			} else {
				body, err := readResponseBody(resp.Body)
				if err == nil {
					f.logger.Debug.Printf("Success Response Body:\n%v\n", body)
					return fmt.Sprintf("Successfully created node (%v)\n", name), err
				} else {
					msg := fmt.Sprintf("Error: %v", err)
					return msg, errors.New(msg)
				}
			}
		}
	}

	return fmt.Sprintf("No update was ran.\n"), err
}

func singularize(plural string) string {
	if singular := regexp.MustCompile(`(.*s)es$`).FindAllStringSubmatch(plural, -1); len(singular) > 0 {
		// ip_address(es), status(es)
		return singular[0][1]
	}
	if singular := regexp.MustCompile(`(.*)s$`).FindAllStringSubmatch(plural, -1); len(singular) > 0 {
		// node(s), vip(s)
		return singular[0][1]
	}
	return plural
}

func (f *NvClient) GetAllFields(object_type string, command map[string][]string, includes map[string][]string, flags []string) (Result, error) {
	fields, err := f.getAllSubsystemNames(object_type)
	if err != nil {
		return nil, err
	}
	m := make(map[string][]string, 0)
	m["include"] = fields
	u := f.getSearchUrl(object_type, command, includes)

	resp, _ := f.httpClient.Get(u)

	f.logger.Debug.Println(fmt.Sprintf("URL: %v", u))

	responseStr, err := readResponseBody(resp.Body)
	if err != nil {
		log.Fatal("Unable to read response body.")
	}

	return f.getResultsFromResponse(responseStr)
}

func intersection(allSubsystemNames []string, fields []string) []string {
	result := make([]string, 0)
	for _, name := range allSubsystemNames {
		for _, inc := range fields {
			if strings.Contains(name, inc) || strings.Contains(inc, name) {
				result = append(result, inc)
			}
		}
	}
	return result
}

func (f *NvClient) getAllSubsystemNames(objectType string) ([]string, error) {
	var err error
	if len(f.subsystemNames) == 0 {
		// query http://opsdb.wc1.yellowpages.com/nodes/field_names.xml
		u := fmt.Sprintf("%v/%v/field_names.xml", f.GetServer(), objectType)

		// store search_shortcuts
		resp, err := f.httpClient.Get(u)
		if err != nil {
			return f.subsystemNames, err
		}
		responseStr, err := readResponseBody(resp.Body)
		if err != nil {
			log.Fatal("Unable to read response body.")
		}
		search_shortcuts.SaveFieldShortcuts(responseStr, "/field_names", "field_name", []string{}...)
		f.subsystemNames, err = f.getSubsystemNamesFromResponse(responseStr)
	}
	return f.subsystemNames, err
}

func (f *NvClient) getSubsystemNamesFromResponse(response string) ([]string, error) {
	ResetShortcuts()

	d, err := libxml2.ParseString(response)
	if err != nil {
		log.Fatal("Unable to parse response as xml:\n%v", response)
	}
	xPathResult, err := d.Find("/field_names")

	set := make(map[string]uint8)
	result := make([]string, 0)
	var found = false
	var iter = xPathResult.NodeIter()
	for iter.Next() {
		found = true
		childNodes, _ := iter.Node().ChildNodes()
		for _, node := range childNodes {
			if node.NodeName() == "field_name" {
				if m := regexp.MustCompile(`^(.*)\[.*\]`).FindAllStringSubmatch(node.NodeValue(), -1); len(m) > 0 {
					// shortcut found
					set[m[0][1]] = 1
				}
			}
		}
	}
	for k := range set {
		result = append(result, k)
	}
	if !found {
		return result, errors.New("No matching objects\n")
	}
	return result, nil
}

func (f *NvClient) getSearchUrl(object_type string, searchCommand map[string][]string, includes map[string][]string) string {
	// start organizing commands issued
	values := url.Values{}
	for k, v := range searchCommand {
		values = mergeMapOfStringArrays(values, separate(v, k))
	}

	for k, v := range includes {
		m := make(map[string][]string, 0)
		for _, f := range v {

			var fieldsRegex = regexp.MustCompile(`([^[]+)\[.+\]`)
			if fieldsRegex.MatchString(f) {
				// field[subfield]
				fieldName := fieldsRegex.FindAllStringSubmatch(f, -1)
				val := []string{""}
				if strings.Contains(f, "[tags]") {
					val = append(val, "tags")
				}
				m[k+"["+fieldName[0][1]+"]"] = val
			} else {
				// field
				m[k+"["+f+"]"] = []string{""}
			}
		}
		values = mergeMapOfStringArrays(values, m)
	}

	return fmt.Sprintf("%v/%v.xml?%v", f.GetServer(), object_type, values.Encode())
}

func (f *NvClient) getSetUrl(object_type string, id string, query string) string {
	return fmt.Sprintf("%v/%v/%v.xml?%v", f.GetServer(), object_type, id, query)
}

func (f *NvClient) getCreateUrl(object_type string, query string) string {
	return fmt.Sprintf("%v/%v.xml?%v", f.GetServer(), object_type, query)
}

func (f *NvClient) getFieldValue(response string) (Result, error) {
	return f.getResultsFromResponse(response)
}

func (f *NvClient) getResultFromDom(node types.Node) (Result, error) {
	rootChildren, err := node.ChildNodes()

	if err != nil {
		return nil, err
	}

	var isArray bool
	var isNil bool
	e, ok := node.(types.Element)
	if ok {
		attr, err := e.GetAttribute("type")
		if err == nil && attr.Value() == "array" {
			isArray = true
		}
		attr, err = e.GetAttribute("nil")
		if err == nil && attr.Value() == "true" {
			isNil = true
		}
	}
	if isNil {
		if isArray {
			return nil, nil
		}
		return &ResultValue{Name: node.NodeName(), Value: ""}, nil
	}

	if isArray {
		// Convert this node to array node
		arr := &ResultArray{Array: make([]Result, 0), Name: node.NodeName()}
		for _, n := range rootChildren {
			switch n.NodeType() {
			case clib.ElementNode:
				arrChild, _ := f.getResultFromDom(n)
				arr.Array = append(arr.Array, arrChild)
			}
		}
		return arr, nil
	}

	result := &ResultMap{Name: node.NodeName()}
	// Looping through each element in search (e.g. <node>)
	for _, n := range rootChildren {
		switch n.NodeType() {
		case clib.ElementNode:
			// traverse down to parse.
			r, _ := f.getResultFromDom(n)
			result.Add(n.NodeName(), r)
		case clib.TextNode:
			// ignore
			if len(rootChildren) == 1 {
				return &ResultValue{Value: n.NodeValue()}, nil
			}
		default:
			f.logger.Warning.Println(fmt.Sprintf("Unknown node type!!! %v %v", n.NodeType(), n.NodeName()))
		}
	}
	return result, nil
}

func (f *NvClient) getResultsFromResponse(response string) (Result, error) {

	d, err := libxml2.ParseString(response)
	if err != nil {
		log.Fatal("Unable to parse response as xml:\n%v", response)
	}

	root, err := d.DocumentElement()
	if err != nil {
		return nil, err
	}

	result, err := f.getResultFromDom(root)
	return result, err
}

func PrintResultsFilterByFields(r Result, fields []string) string {
	result := ""
	if r == nil {
		return "No matching objects\n"
	}
	if len(fields) == 0 {
		// Just print the names, no fields specified
		switch t := r.(type) {
		case *ResultArray:
			if len(t.Array) == 0 {
				return "No matching objects\n"
			}
			for _, elm := range t.Array {
				switch ct := elm.(type) {
				case *ResultMap:
					dt, ok := ct.Get("name").(*ResultValue)
					if ok {
						result += dt.Value + "\n"
					} else {
						result += ct.Name + "\n"
					}
				}
			}
		case *ResultMap:
			result += t.Name + "\n"
		}
	} else {
		// Fields specified, print name, plus fields specified.
		// Just print the names, no fields specified
		switch t := r.(type) {
		case *ResultArray:
			if len(t.Array) == 0 {
				return "No matching objects\n"
			}
			for _, elm := range t.Array {
				switch ct := elm.(type) {
				case *ResultMap:
					dt, ok := ct.Get("name").(*ResultValue)
					if ok {
						result += dt.Value + ":\n"
					}
					result += PrintResultsFilterByFieldsRecursive(ct, "", fields) + "\n"
				}
			}
		case *ResultMap:
			dt, ok := t.Get("name").(*ResultValue)
			if ok {
				result += dt.Value + "\n"
			}
		}
	}
	return result
}

func PrintResultsFilterByFieldsRecursive(r Result, parent string, fields []string) string {
	result := ""
	// Just print the names, no fields specified
	switch t := r.(type) {
	case *ResultArray:
		if shouldPrint(parent, fields) {
			fields = append(fields, combineName(parent, "name"))
		}
		for _, elm := range t.Array {
			switch elm.(type) {
			case *ResultArray:
				result += PrintResultsFilterByFieldsRecursive(elm, parent, fields)
			case *ResultMap:
				result += PrintResultsFilterByFieldsRecursive(elm, parent, fields)
			default:
				result += PrintResultsFilterByFieldsRecursive(elm, parent, fields)
			}
		}
	case *ResultMap:
		for _, k := range t.GetOrder() {
			v := t.Get(k)
			switch ct := v.(type) {
			case *ResultValue:
				name := combineName(parent, k)
				if shouldPrint(name, fields) || shouldPrint(k, fields) {
					result += name + ": " + PrintResultsFilterByFieldsRecursive(ct, parent, fields) + "\n"
				}
			default:
				name := combineName(parent, k)
				result += PrintResultsFilterByFieldsRecursive(ct, name, fields)
			}
		}
	case *ResultValue:
		result += t.Value
	}
	return result
}

func combineName(parent, name string) string {
	if len(parent) == 0 {
		return name
	} else {
		return parent + "[" + name + "]"
	}
}

func shouldPrint(name string, fields []string) bool {
	if len(fields) == 0 {
		return true
	} else {
		for _, f := range fields {
			if !strings.Contains(name, "[") && strings.Contains(name, f) {
				return true
			} else if name == f {
				return true
			} else if f == "*" {
				return true
			}
		}
	}
	return false
}

func DebugPrintResults(r Result, parent string) string {
	result := "(" + r.ID() + ")"
	switch r := r.(type) {
	case *ResultMap:
		result = "Map: " + result
		if len(r.GetOrder()) == 0 {
		} else {
			for _, k := range r.GetOrder() {
				v := r.Get(k)
				if v == nil {
					result += combineName(parent, k) + ":\n"
				} else {
					result += DebugPrintResults(v, combineName(parent, k))
				}
			}
		}
	case *ResultArray:
		result = "Array: " + result
		for _, v := range r.Array {
			result += DebugPrintResults(v, parent)
		}
	case *ResultValue:
		result = "Value: " + result
		result += parent + ": " + r.Value + "\n"
	}
	return result
}

func PrintResults(r Result) string {
	result := ""
	switch r := r.(type) {
	case *ResultMap:
		//result += r.Name
		if len(r.GetOrder()) == 0 {
			result += "\n"
		} else {
			result += ":\n"
			for _, k := range r.GetOrder() {
				v := r.Get(k)
				result += k + ": " + PrintResultsRecursive(v, k) + "\n"
			}
		}
	case *ResultArray:
		for _, v := range r.Array {
			m, ok := v.(*ResultMap)
			if ok {
				name := m.Get("name")
				n, ok := name.(*ResultValue)
				if ok {
					result += n.Value + ":\n"
				}
			}
			result += PrintResultsRecursive(v, "") + "\n"
		}
	case *ResultValue:
		result += r.Value
	}
	return result
}
func PrintResultsRecursive(r Result, parent string) string {
	result := ""
	switch r := r.(type) {
	case *ResultMap:
		//result += r.Name
		if len(r.GetOrder()) == 0 {
		} else {
			for _, k := range r.GetOrder() {
				v := r.Get(k)
				if v == nil {
					result += combineName(parent, k) + ":\n"
				} else {
					result += PrintResultsRecursive(v, combineName(parent, k))
				}
			}
		}
	case *ResultArray:
		for _, v := range r.Array {
			result += PrintResultsRecursive(v, parent)
		}
	case *ResultValue:
		result += parent + ": " + r.Value + "\n"
	}
	return result
}

func getChildFieldValue(node types.Node, parent string, fields []string) map[string]string {
	result := make(map[string]string, 0)
	if node.NodeType() == clib.TextNode {
	} else {
		// Non-text child node
		nl, err := node.ChildNodes()
		if len(nl) == 0 && err == nil {
			x := node.(types.Element)
			if attr, err := x.GetAttribute("type"); err == nil && attr.Value() == "array" {
				return result
			}
			// No more child
			fieldName := ""
			if parent == "" {
				fieldName = node.NodeName()
			} else {
				fieldName = parent + "[" + node.NodeName() + "]"
			}
			if addFieldExists(fieldName, fields) {
				result[fieldName] = node.NodeValue()
			}
		} else if len(nl) == 1 && nl.First().NodeType() == clib.TextNode {
			fieldName := ""
			// Only text child left
			if parent == "" {
				fieldName = node.NodeName()
			} else {
				fieldName = parent + "[" + node.NodeName() + "]"
			}
			if addFieldExists(fieldName, fields) {
				result[fieldName] = node.NodeValue()
			}
		} else {
			// Have child nodes.
			for _, n := range nl {
				if strings.Contains(parent, node.NodeName()) {
					result = mergeMapOfStrings(result, getChildFieldValue(n, parent, fields))
				} else if parent == "" {
					result = mergeMapOfStrings(result, getChildFieldValue(n, node.NodeName(), fields))
				} else {
					result = mergeMapOfStrings(result, getChildFieldValue(n, parent+"["+node.NodeName()+"]", fields))
				}
			}
		}
	}
	return result
}

func addFieldExists(field string, filter []string) bool {
	for _, f := range filter {
		if f == field || f == "*" {
			return true
		}
	}
	return false
}

func mergeMapOfStringArrays(a map[string][]string, b map[string][]string) map[string][]string {
	result := make(map[string][]string)
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = append(result[k], v...)
	}
	return result
}

func mergeMapOfStrings(a map[string]string, b map[string]string) map[string]string {
	result := make(map[string]string)
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
}

func separate(hashStrings []string, prefix string) map[string][]string {
	hash := make(map[string][]string)
	// name[key]=value1,map[key]=value2
	for _, val := range hashStrings {
		values := strings.Split(val, ",")
		// name[key]=value
		for _, value := range values {
			pair := strings.Split(value, "=")
			key := ""
			value := ""
			if len(pair) == 2 {
				key = fmt.Sprintf("%v%v", prefix, search_shortcuts.Replace(pair[0]))
				value = pair[1]
			} else if len(pair) == 1 {
				key = "name"
				value = pair[0]
			} else {
				break
			}

			hash[key] = []string{value}
		}
	}
	return hash
}

func NoRedirectFunc(req *http.Request, via []*http.Request) error {
	return errors.New("No redirect.")
}

func RedirectFunc(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

func isRedirectResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	return isRedirect(resp.StatusCode)
}

func isRedirect(returnCode int) bool {
	if returnCode >= 300 && returnCode < 400 {
		return true
	}
	return false
}

func handleResponseError(err error) error {
	// return true - error happened
	// false - no error
	if strings.Contains(fmt.Sprintf("%v", err), "No redirect.") {
		// If we're redirected only, return nil
		return nil
	}
	return err
}

func getHeaderLocation(resp *http.Response) string {
	if resp != nil && resp.Header["Location"] != nil {
		return resp.Header["Location"][0]
	}
	return ""
}

func readResponseBody(body io.ReadCloser) (string, error) {
	//defer body.Close()
	contents, err := ioutil.ReadAll(body)
	if err != nil {
		return "", err
	}
	return string(contents), nil
}
