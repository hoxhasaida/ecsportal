package main

import (
	"bytes"
	"crypto/tls"
	"encoding/xml"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"github.com/unrolled/render"
)

var rendering *render.Render
var store = sessions.NewCookieStore([]byte("session-key"))

func contains(dict map[string]string, i string) bool {
	if _, ok := dict[i]; ok {
		return true
	} else {
		return false
	}
}

func int64toString(value int64) string {
	return strconv.FormatInt(value, 10)
}

func int64InSlice(i int64, list []int64) bool {
	for _, value := range list {
		if value == i {
			return true
		}
	}
	return false
}

type appError struct {
	err      error
	status   int
	json     string
	template string
	binding  interface{}
}

type appHandler func(http.ResponseWriter, *http.Request) *appError

func (fn appHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if e := fn(w, r); e != nil {
		log.Print(e.err)
		if e.status != 0 {
			if e.json != "" {
				rendering.JSON(w, e.status, e.json)
			} else {
				rendering.HTML(w, e.status, e.template, e.binding)
			}
		}
	}
}

func RecoverHandler(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				http.Error(w, http.StatusText(500), 500)
			}
		}()

		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

func LoginMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" || strings.HasPrefix(r.URL.Path, "/app") {
			h.ServeHTTP(w, r)
		} else {
			session, err := store.Get(r, "session-name")
			if err != nil {
				rendering.HTML(w, http.StatusInternalServerError, "error", http.StatusInternalServerError)
			}
			if _, ok := session.Values["AccessKey"]; ok {
				h.ServeHTTP(w, r)
			} else {
				http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
			}
		}
	})
}

type ECS struct {
	Hostname  string `json:"hostname"`
	EndPoint  string `json:"endpoint"`
	Namespace string `json:"namespace"`
}

var hostname string

func main() {
	var port = ""
	// get all the environment data
	port = "8001" //os.Getenv("PORT")

	hostname, _ = os.Hostname()

	// See http://godoc.org/github.com/unrolled/render
	rendering = render.New(render.Options{Directory: "app/templates"})

	// See http://www.gorillatoolkit.org/pkg/mux
	router := mux.NewRouter()
	router.HandleFunc("/", Index)
	router.HandleFunc("/login", Login)
	router.HandleFunc("/logout", Logout)
	router.PathPrefix("/app/").Handler(http.StripPrefix("/app/", http.FileServer(http.Dir("app"))))

	n := negroni.Classic()
	n.UseHandler(RecoverHandler(LoginMiddleware(router)))
	n.Run(":" + port)

	log.Printf("Listening on port " + port)
}

type UserSecretKeysResult struct {
	XMLName    xml.Name `xml:"user_secret_keys"`
	SecretKey1 string   `xml:"secret_key_1"`
	SecretKey2 string   `xml:"secret_key_2"`
}

type UserSecretKeyResult struct {
	XMLName   xml.Name `xml:"user_secret_key"`
	SecretKey string   `xml:"secret_key"`
}

type credentials struct {
	AccessKey  string
	SecretKey1 string
	SecretKey2 string
}

var tpl *template.Template

func init() {
	tpl = template.Must(template.ParseFiles("app/templates/index.tmpl"))
}

// Login using an AD or object user
func Login(w http.ResponseWriter, r *http.Request) {
	// If informaton received from the form
	if r.Method == "POST" {
		session, err := store.Get(r, "session-name")
		if err != nil {
			rendering.HTML(w, http.StatusInternalServerError, "error", http.StatusInternalServerError)
		}

		
		r.ParseForm()
		authentication := r.FormValue("authentication")
		user := r.FormValue("user")
		password := r.FormValue("password")
		endpoint := r.FormValue("endpoint")
		namespace := r.FormValue("namespace")
		// For AD authentication, needs to retrieve the S3 secret key from ECS using the ECS management API
		if authentication == "ad" { //ktu ndodh  ekzekutimi i kodit
			url, err := url.Parse(endpoint)
			if err != nil {
				rendering.HTML(w, http.StatusOK, "login", "Check the endpoint")
			}
			hostname := url.Host
			if strings.Contains(hostname, ":") {
				hostname = strings.Split(hostname, ":")[0]
			}
			tr := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
			client := &http.Client{Transport: tr}
			// Get an authentication token from ECS
			req, _ := http.NewRequest("GET", "https://"+hostname+":4443/login", nil)
			req.SetBasicAuth(user, password)
			resp, err := client.Do(req)
			if err != nil {
				log.Print(err)
			}
			if resp.StatusCode == 401 {
				rendering.HTML(w, http.StatusOK, "login", "Check your crententials and that you're allowed to generate a secret key on ECS")
			} else {
				// Get the secret key from ECS
				req, _ = http.NewRequest("GET", "https://"+hostname+":4443/object/secret-keys", nil)
				headers := map[string][]string{}
				headers["X-Sds-Auth-Token"] = []string{resp.Header.Get("X-Sds-Auth-Token")}
				req.Header = headers
				log.Print(headers)
				resp, err = client.Do(req)
				if err != nil {
					log.Print(err)
				}
				buf := new(bytes.Buffer)
				buf.ReadFrom(resp.Body)
				secretKey := ""
				userSecretKeysResult := &UserSecretKeysResult{}
				xml.NewDecoder(buf).Decode(userSecretKeysResult)
				secretKey = userSecretKeysResult.SecretKey1

				// If a secret key doesn't exist yet for this object user, needs to generate it
				if secretKey == "" {
					req, _ = http.NewRequest("POST", "https://"+hostname+":4443/object/secret-keys", bytes.NewBufferString("<secret_key_create_param></secret_key_create_param>"))
					headers["Content-Type"] = []string{"application/xml"}
					req.Header = headers
					resp, err = client.Do(req)
					if err != nil {
						log.Print(err)
					}
					buf = new(bytes.Buffer)
					buf.ReadFrom(resp.Body)
					userSecretKeyResult := &UserSecretKeyResult{}
					xml.NewDecoder(buf).Decode(userSecretKeyResult)
					secretKey = userSecretKeyResult.SecretKey
				}
				log.Print(secretKey)
				session.Values["AccessKey"] = user
				session.Values["SecretKey"] = secretKey
				session.Values["Endpoint"] = endpoint
				session.Values["Namespace"] = namespace
				p := credentials{
					AccessKey:  user,
					SecretKey1: secretKey,
					SecretKey2: userSecretKeysResult.SecretKey2,
				}
				err = sessions.Save(r, w)
				if err != nil {
					rendering.HTML(w, http.StatusInternalServerError, "error", http.StatusInternalServerError)
				}
				rendering.HTML(w, http.StatusOK, "index", p)
			}
			// For an object user authentication, use the credentials as-is
		} else {
			session.Values["AccessKey"] = user
			session.Values["SecretKey"] = password
			session.Values["Endpoint"] = endpoint
			session.Values["Namespace"] = namespace
			p := credentials{
				AccessKey:  user,
				SecretKey1: password,
				SecretKey2: "",
			}
			err = sessions.Save(r, w)
			if err != nil {
				rendering.HTML(w, http.StatusInternalServerError, "error", http.StatusInternalServerError)
			}
			rendering.HTML(w, http.StatusOK, "index", p)
		}
	} else {
		rendering.HTML(w, http.StatusOK, "login", nil)
	}
}

func Logout(w http.ResponseWriter, r *http.Request) {
	session, err := store.Get(r, "session-name")
	if err != nil {
		rendering.HTML(w, http.StatusInternalServerError, "error", http.StatusInternalServerError)
	}
	delete(session.Values, "AccessKey")
	delete(session.Values, "SecretKey")
	delete(session.Values, "Endpoint")
	delete(session.Values, "Namespace")
	err = sessions.Save(r, w)

	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

func Index(w http.ResponseWriter, r *http.Request) {
	rendering.HTML(w, http.StatusOK, "login", nil)
}
