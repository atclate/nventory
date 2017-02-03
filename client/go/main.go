package main

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

var (
	httpClientMap map[string]*http.Client
	logger        Log
	autoreg       string
	server        string
	newOpsDB      bool
)

func main() {
	autoreg := "autoreg"
	autoreg_password := "qq8Erkee&T"
	server = "http://dev-opsdb.np.wc1.yellowpages.com"

	InitLogger(&logger, ioutil.Discard, os.Stdout, os.Stdout, os.Stderr, os.Stdout)
	client, err := GetHttpClientFor(server, autoreg, func() string { return autoreg_password })
	if err != nil {
		log.Fatal("Unable to initialize HTTP Client")
	}
	nv := NewNvClient(client, server, func() string { return autoreg_password }, &logger)

	m := make(map[string][]string)
	m[""] = []string{"acheung."}
	i := make(map[string][]string)
	nv.GetObjects("nodes", m, i, "autoreg", func() string { return autoreg_password })
}

func GetHttpClientFor(host, login string, passwordCallback func() string) (*http.Client, error) {
	if httpClientMap == nil {
		httpClientMap = make(map[string]*http.Client, 0)
	}
	// Check if client is already initialized.
	httpClient := httpClientMap[login]
	if httpClient != nil {
		return httpClient, nil
	}

	// Load from cookie file
	cookie_file := getCookieFilename(login)

	password := passwordCallback()
	cookies, err := loadCookies(cookie_file)
	if err == nil && len(cookies) > 0 {
		logger.Debug.Printf("Loading from cookie file (%v)", cookie_file)
		httpClient = createBlankHttpClientFor(login, password)
		logger.Debug.Printf("cookie host: %v\n", cookies[0].Domain)
		u := &url.URL{
			Scheme: "http",
			Host:   cookies[0].Domain,
		}
		server = u.String()
		u.Path = cookies[0].Path
		if err == nil {
			httpClient.Jar.SetCookies(u, cookies)
		}
	}
	httpClient, err = createHttpClientFor(host, login, password, httpClient)
	if err != nil {
		// Failed creating new http client.
		logger.Error.Printf("Failed creating http client. Error: %v", err)
		os.Exit(1)
	}

	httpClientMap[login] = httpClient

	return httpClient, err
}

func createBlankHttpClientFor(login, password string) *http.Client {
	options := cookiejar.Options{
		PublicSuffixList: publicsuffix.List,
	}
	cookieJar, _ := cookiejar.New(&options)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Jar:           cookieJar,
		Timeout:       time.Duration(0),
		CheckRedirect: RedirectFunc,
		Transport:     tr,
	}
	return client
}

func createHttpClientFor(host, login, password string, httpClient *http.Client) (*http.Client, error) {
	if httpClient == nil {
		httpClient = createBlankHttpClientFor(login, password)
	}

	redirflag := false
	username := login
	passwd := password

	httpClient.CheckRedirect = NoRedirectFunc
	vFoo := url.Values{}
	vFoo.Set("foo", "bar")
	urlStr := fmt.Sprintf("%v/accounts.xml", host)
	logger.Debug.Printf("posting to (%v)", urlStr)
	resp, err := httpClient.Post(urlStr, "application/x-www-form-urlencoded", strings.NewReader(vFoo.Encode()))
	if err == nil {
		respStr, err := readResponseBody(resp.Body)
		if err == nil {
			if isRedirectResponse(resp) {
				logger.Debug.Printf("response %v redirected to %v", urlStr, getHeaderLocation(resp))
			} else {
				logger.Debug.Printf("response from %v:\n%v", urlStr, respStr)
			}
		} else {
			logger.Debug.Printf("error from %v:\n%v", urlStr, err)
		}
	} else if handleResponseError(err) != nil {
		log.Fatal(fmt.Sprintf("err: %v", err))
	}

	responseCode := resp.StatusCode
	if isRedirect(responseCode) {
		var isSSO = regexp.MustCompile(`^https:\/\/sso.*`)
		var isAuthorized = regexp.MustCompile(`^(http|https):\/\/(sso.*)\/session\/tokens`)

		if username != autoreg {
			// Follow all redirects for nginx cause POST doesn't
			for isRedirectResponse(resp) && !isSSO.MatchString(getHeaderLocation(resp)) {
				urlStr = getHeaderLocation(resp)
				logger.Debug.Printf("Posting to: %v\n", urlStr)
				resp, err = httpClient.Post(urlStr, "application/x-www-form-urlencoded", strings.NewReader(vFoo.Encode()))
			}
			cookieLocation := urlStr
			if location := getHeaderLocation(resp); isRedirectResponse(resp) && isSSO.MatchString(location) {
				logger.Debug.Printf("POST to %v/accounts.xml was redirected, authenticating to SSO\n", host)
				redirflag = true
				numRedirects := 1

				logger.Debug.Printf("Login: %v\n", username)
				if passwd == "" {
					username, passwd, err = PromptUserLogin(username, bufio.NewReader(os.Stdin))
				}

				// is sso
				// TODO: if no password exists, use password callback (what is passed in)
				numRedirects = 0
				for redirflag && numRedirects < 7 {
					var sso_server string
					ssoServerUrl, err := url.Parse(location)
					if err == nil {
						sso_server = ssoServerUrl.Host
					}
					logger.Debug.Println(fmt.Sprintf("SSO_SERVER: %v\n", sso_server))

					v := url.Values{}
					v.Set("login", username)
					v.Set("password", passwd)
					urlStr = fmt.Sprintf("https://%v/login?noredirects=1", sso_server)
					fmt.Printf("Authenticating to %v...\n", urlStr)
					resp, err = httpClient.Post(urlStr, "application/x-www-form-urlencoded", strings.NewReader(v.Encode()))
					responseCode = resp.StatusCode
					logger.Debug.Printf("Response: %v", resp)
					location = getHeaderLocation(resp)
					if isRedirectResponse(resp) {
						logger.Debug.Printf("redirect location: %v", location)
					}

					if responseCode == 200 || (isRedirect(responseCode) && isAuthorized.MatchString(location)) {
						logger.Debug.Printf("Authentication Successful to %v\n", cookieLocation)
						urlObj, err := url.Parse(cookieLocation)
						if err == nil {
							cookie_file := getCookieFilename(username)
							logger.Debug.Printf("Saving to cookie file (%v)", cookie_file)
							saveCookie(httpClient.Jar.Cookies(urlObj), urlObj.Host, cookie_file)
						}
						redirflag = false
					} else if isRedirect(responseCode) {
						logger.Debug.Println(fmt.Sprintf("Redirected to %v\n", getHeaderLocation(resp)))
					} else {
						var isCantConnect = regexp.MustCompile(`Can't connect .* Invalid argument`)
						responseStr, err := readResponseBody(resp.Body)
						if err != nil {
							logger.Debug.Println("Unable to read response body.")
							return nil, errors.New("Unable to read response body.")
						}
						if isCantConnect.MatchString(responseStr) {
							logger.Debug.Println("Looks like you're missing Crypt::SSLeay")
							return nil, errors.New("Cannot connect. Looks like you're missing Crypt::SSLeay")
						}
						return nil, errors.New(fmt.Sprintf("Authentication failed:\n%v", resp))
					}
					// scheme => https
					numRedirects++
				}
				if numRedirects == 7 {
					return nil, errors.New("SSO redirect loop")
				}
			}
			if isRedirectResponse(resp) && isAuthorized.MatchString(getHeaderLocation(resp)) {
				tr := &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				}
				getClient := http.Client{
					CheckRedirect: func(req *http.Request, via []*http.Request) error {
						if len(via) >= 2 {
							return errors.New("stopped after 2 redirects")
						}
						return nil
					},
					Jar:       httpClient.Jar,
					Transport: tr,
				}
				resp, err = getClient.Get(getHeaderLocation(resp))
				if handleResponseError(err) != nil {
					return nil, errors.New(fmt.Sprintf("fatal error: %v", err))
				}
				if resp.StatusCode != 422 {
					if resp.StatusCode == 200 {
						return nil, errors.New("Unable to get SSO session token.  Might be authentication failure or SSO problem\n")
					}
				}
			}
		} else {
			// is autoreg
			var isSSORedir = regexp.MustCompile(`^https:\/\/sso.*\/login\?url`)
			var isSSORedirSearch = regexp.MustCompile(`^https:\/\/(sso.*)\/login\?url`)

			var nonSSOLocation string

			for err == nil && isRedirectResponse(resp) && !isSSORedir.MatchString(getHeaderLocation(resp)) {
				urlStr = getHeaderLocation(resp)
				nonSSOLocation = urlStr
				urlObj, err := url.Parse(urlStr)
				if err == nil {
					server = urlObj.Scheme + "://" + urlObj.Host
				}
				resp, err = httpClient.Post(urlStr, "application/x-www-form-urlencoded", strings.NewReader(vFoo.Encode()))
				if err == nil {
					respStr, err := readResponseBody(resp.Body)
					if err == nil {
						if isRedirectResponse(resp) {
							logger.Debug.Printf("response %v redirected to %v", urlStr, getHeaderLocation(resp))
						} else {
							logger.Debug.Printf("response(%v) from %v:\n%v", resp.StatusCode, urlStr, respStr)
						}
					}

				}
			}

			if isSSORedirSearch.MatchString(getHeaderLocation(resp)) {
				logger.Debug.Println(fmt.Sprintf("POST to %v/accounts.xml ( ** for user 'autoreg' ** ) was redirected, authenticating to local login path: '/login/login'\n", host))

				var urlBase string
				if nonSSOLocation != "" {
					urlBaseObj, err := url.Parse(nonSSOLocation)
					if err == nil {
						urlBase = fmt.Sprintf("https://%v", urlBaseObj.Host)
					}
				} else {
					urlBase = host
				}
				urlStr = fmt.Sprintf("%v/login/login", urlBase)
				urlObj, err := url.Parse(urlStr)
				if err != nil {
					log.Fatal(fmt.Sprintf("Error when parsing URL %v: %v", urlStr, err))
				}
				urlObj.Scheme = "https"
				urlStr = urlObj.String()
				logger.Debug.Println(fmt.Sprintf("Authenticating to %v", urlStr))

				v := url.Values{}
				v.Set("login", username)
				v.Set("password", passwd)
				httpClient.CheckRedirect = RedirectFunc
				resp, err = httpClient.Post(urlStr, "application/x-www-form-urlencoded", strings.NewReader(v.Encode()))
				if err != nil {
					log.Fatal(fmt.Sprintf("Error when posting to %v: %v", urlStr, err))
				}

				cookie_file := getCookieFilename(username)
				logger.Debug.Printf("Saving to cookie file (%v)", cookie_file)
				saveCookie(httpClient.Jar.Cookies(urlObj), urlObj.Host, cookie_file)

				_, _ = readResponseBody(resp.Body)
				//				respStr, err := readResponseBody(resp.Body)
				//				logger.Debug.Println(fmt.Sprintf("Response Status: %v\nResponse Body: %v\nResponse Err: %v", resp.StatusCode, respStr))
			} else {
				logger.Debug.Printf("Authentication successful.\n")
			}
		}
	}
	httpClient.CheckRedirect = RedirectFunc

	return httpClient, nil
}

func saveCookie(cookies []*http.Cookie, domain, filename string) {
	logger.Debug.Printf("cookie file: %v, cookies: %v, domain: %v", filename, cookies, domain)
	if len(cookies) < 1 {
		return
	}
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		// File doesn't exist. Create.
		f, err := os.Create(filename)
		defer f.Close()
		if err == nil {
			for _, c := range cookies {
				if c.Path == "" {
					c.Path = "/"
				}
				if c.Domain == "" {
					c.Domain = domain
				}
				cJson, err := serializeJSON(c)
				if err == nil {
					logger.Debug.Printf("cookie json: %v", cJson)
					// Save to file
					res, err := f.WriteString(cJson + "\n")
					if err != nil {
						logger.Error.Printf("Error writing to cookie file (%v) [%v]: %v\n", filename, res, err)
					}
				}
			}
		} else {
			logger.Error.Printf("Error creating cookie file (%v): %v\n", filename, err)
		}
	} else {
		// File already exists. Just overwrite.
		f, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0600)
		defer f.Close()
		if err == nil {
			for _, c := range cookies {
				if c.Path == "" {
					c.Path = "/"
				}
				if c.Domain == "" {
					c.Domain = domain
				}
				cJson, err := serializeJSON(c)
				if err == nil {
					logger.Debug.Printf("writing cookie json: %v", cJson)
					// Save to file
					res, err := f.WriteString(cJson + "\n")
					if err != nil {
						logger.Error.Printf("Error writing to cookie file (%v) [%v]: %v\n", filename, res, err)
					}
				}
			}
		} else {
			logger.Error.Printf("Error openning cookie file (%v): %v\n", filename, err)
		}
	}
}

func loadCookies(filename string) (c []*http.Cookie, err error) {
	res := make([]*http.Cookie, 0)

	// TODO: Check if previous cookie is already there
	_, err = os.Stat(filename)
	if err == nil {
		// cookie file found
		//Read cookie

		f, err := os.Open(filename)
		if err == nil {
			defer f.Close()
			reader := bufio.NewReader(f)
			line, _ := readLine(reader)
			for line != "" {
				cookie := &http.Cookie{}
				err = deserializeJSONCookie(string(line), cookie)
				if err == nil {
					logger.Debug.Printf("Cookie Found at %v: %v=%v", filename, cookie.Name, cookie.Value)
					res = append(res, cookie)
				}
				line, _ = readLine(reader)
			}
		}
	}
	return res, err
}

/*** Don't include ***/

func getCookieFilename(login string) string {
	home := os.Getenv("HOME")
	filename := ".opsdb_cookie"
	if newOpsDB {
		filename = ".techopsdb_cookie"
	} else {
		if login == autoreg {
			filename += "_" + login
		}
	}
	return path.Join(home, filename)
}
