package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"

	"code.google.com/p/go-uuid/uuid"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
)

const (
	listenAddr = ":5000"
	tmpDir     = "/tmp/"

	timeout  = 30
	interval = 2

	iconS  = 32
	iconM  = 64
	iconL  = 128
	imageS = 128
	imageM = 256
	imageL = -1
)

var (
	dbConn *sql.DB
	config *Config
)

type Config struct {
	Database struct {
		Dbname   string `json:"dbname"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"database"`
	Datadir string `json:"data_dir"`
}

type User struct {
	Id     int
	Name   string
	Apikey string
	Icon   string
}

type Entry struct {
	Id           int
	User         int
	Image        string
	PublishLevel int
	CreatedAt    string
}

type FollowMap struct {
	User      int
	Target    int
	CreatedAt string
}

type Response map[string]interface{}

func (r Response) String() (s string) {
	b, err := json.Marshal(r)
	if err != nil {
		s = ""
	} else {
		s = string(b)
	}
	return
}

func prepareHandler(w http.ResponseWriter, r *http.Request) (baseUrl *url.URL) {
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		baseUrl, _ = url.Parse("http://" + h)
	} else {
		baseUrl, _ = url.Parse("http://" + r.Host)
	}
	return baseUrl
}

func getUser(r *http.Request) (*User, error) {
	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" {
		c, err := r.Cookie("api_key")
		if err != nil {
			return nil, nil
		} else {
			apiKey = c.Value
		}
	}

	user := User{}
	err := dbConn.QueryRow(
		"SELECT * FROM users WHERE api_key = ?", apiKey,
	).Scan(
		&user.Id, &user.Name, &user.Apikey, &user.Icon,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &user, nil
}

func loadConfig(filename string) *Config {
	log.Printf("loading config file: %s", filename)
	f, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	var config Config
	err = json.Unmarshal(f, &config)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	return &config
}

var imgconvdir = flag.String("imgconvdir", "", "directory to convert images")
var iconconvdir = flag.String("iconconvdir", "", "directory to convert images")

func main() {
	flag.Parse()
	if *imgconvdir != "" {
		log.Printf("convert image in directory %s", *imgconvdir)
		convertImages(*imgconvdir)
	}
	if *iconconvdir != "" {
		log.Printf("convert icons in directory %s", *iconconvdir)
		convertIcons(*iconconvdir)
	}
	log.Printf("done")
}

func convertImages(imgDir string) {
	files, err := ioutil.ReadDir(imgDir)
	if err != nil {
		panic(err.Error())
	}

	parallel := 4

	ch := make(chan string, parallel)

	var wg sync.WaitGroup
	defer wg.Wait()

	for n := 0; n < parallel; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range ch {
				makeImageThumbnails(imgDir, name)
			}
		}()
	}

	for _, f := range files {
		ch <- f.Name()
	}
	close(ch)
}

func convertIcons(imgDir string) {
	files, err := ioutil.ReadDir(imgDir)
	if err != nil {
		panic(err.Error())
	}

	parallel := 4

	ch := make(chan string, parallel)

	var wg sync.WaitGroup
	defer wg.Wait()

	for n := 0; n < parallel; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range ch {
				makeIconThumbnails(imgDir, name)
			}
		}()
	}

	for _, f := range files {
		ch <- f.Name()
	}
	close(ch)
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %s", err)
	code := http.StatusInternalServerError
	http.Error(w, http.StatusText(code), code)
}

func notFound(w http.ResponseWriter) {
	code := http.StatusNotFound
	http.Error(w, http.StatusText(code), code)
}

func badRequest(w http.ResponseWriter) {
	code := http.StatusBadRequest
	http.Error(w, http.StatusText(code), code)
}

func join(a ...interface{}) string {
	var ret string
	for _, v := range a {
		ret += fmt.Sprintf("%v", v)
	}
	return ret
}

func sha256Hex(a ...interface{}) string {
	hash := sha256.New()
	hash.Write([]byte(join(a...)))
	md := hash.Sum(nil)
	return hex.EncodeToString(md)
}

func convert(path string, ext string, w int, h int) ([]byte, error) {
	f, err := ioutil.TempFile(tmpDir, "isucon")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())

	newFile := fmt.Sprintf("%s.%s", f.Name(), ext)

	cmd := exec.Command("convert", "-geometry", fmt.Sprintf("%dx%d", w, h), path, newFile)
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	defer os.Remove(newFile)

	b, err := ioutil.ReadFile(newFile)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func resizeToFile(path string, ext string, suffix string, w int, h int) {
	newFile := fmt.Sprintf("%s.%s.%s", path, suffix, ext)
	cmd := exec.Command("convert", "-geometry", fmt.Sprintf("%dx%d", w, h), path, newFile)
	if err := cmd.Run(); err != nil {
		panic(err.Error())
	}
}

func cropSquare(orig string, ext string) (string, error) {
	str, err := exec.Command("identify", orig).Output()
	if err != nil {
		return "", err
	}
	size := regexp.MustCompile(" +").Split(string(str), 4)[2]
	wh := regexp.MustCompile("x").Split(size, 2)
	w, _ := strconv.Atoi(wh[0])
	h, _ := strconv.Atoi(wh[1])
	var crop_x float32
	var crop_y float32
	var pixels int
	if w > h {
		pixels = h
		crop_x = (float32(w-pixels) / 2)
		crop_y = 0
	} else if w < h {
		pixels = w
		crop_x = 0
		crop_y = (float32(h-pixels) / 2)
	} else {
		pixels = w
		crop_x = 0
		crop_y = 0
	}
	f, err := ioutil.TempFile(tmpDir, "isucon")
	if err != nil {
		return "", err
	}
	os.Remove(f.Name())

	newFile := fmt.Sprintf("%s.%s", f.Name(), ext)
	cmd := exec.Command("convert", "-crop", fmt.Sprintf("%vx%v+%v+%v", pixels, pixels, crop_x, crop_y), orig, newFile)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return newFile, nil
}

func renderJson(w http.ResponseWriter, r Response) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, r)
}

func renderJsonNoCache(w http.ResponseWriter, r Response) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, r)
}

func signupHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	name := r.FormValue("name")

	if !regexp.MustCompile("^[a-zA-Z0-9_]{2,16}$").MatchString(name) {
		badRequest(w)
		return
	}

	apiKey := sha256Hex(uuid.NewUUID())

	result, err := dbConn.Exec(
		"INSERT INTO users (name, api_key, icon) VALUES (?, ?, ?)",
		name, apiKey, "default",
	)
	if err != nil {
		serverError(w, err)
		return
	}

	id, _ := result.LastInsertId()
	user := User{}
	err = dbConn.QueryRow(
		"SELECT * FROM users WHERE id = ?", id,
	).Scan(
		&user.Id, &user.Name, &user.Apikey, &user.Icon,
	)
	if err != nil {
		serverError(w, err)
		return
	}

	renderJson(w, Response{
		"id":      user.Id,
		"name":    user.Name,
		"api_key": user.Apikey,
		"icon":    baseUrl.String() + "/icon/" + user.Icon,
	})
}

func meHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	renderJson(w, Response{
		"id":   user.Id,
		"name": user.Name,
		"icon": baseUrl.String() + "/icon/" + user.Icon,
	})
}

func entryHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	uploadFile, handler, err := r.FormFile("image")
	if err != nil {
		serverError(w, err)
		return
	}
	if handler == nil {
		badRequest(w)
		return
	}

	contentType := handler.Header.Get("Content-Type")
	if !regexp.MustCompile("^image/jpe?g").MatchString(contentType) {
		badRequest(w)
		return
	}

	data, err := ioutil.ReadAll(uploadFile)
	if err != nil {
		serverError(w, err)
		return
	}

	imageId := sha256Hex(uuid.NewUUID())
	err = ioutil.WriteFile(config.Datadir+"/image/"+imageId+".jpg", data, 0666)
	if err != nil {
		serverError(w, err)
		return
	}

	publishLevel := r.FormValue("publish_level")
	result, err := dbConn.Exec(
		"INSERT INTO entries (user, image, publish_level, created_at) VALUES (?, ?, ?, NOW())",
		user.Id, imageId, publishLevel,
	)
	if err != nil {
		serverError(w, err)
		return
	}

	id, err := result.LastInsertId()
	if err != nil {
		serverError(w, err)
		return
	}

	entry := Entry{}
	err = dbConn.QueryRow(
		"SELECT * FROM entries WHERE id = ?", id,
	).Scan(
		&entry.Id, &entry.User, &entry.Image, &entry.PublishLevel, &entry.CreatedAt,
	)
	if err != nil {
		serverError(w, err)
		return
	}

	renderJson(w, Response{
		"id":            entry.Id,
		"image":         baseUrl.String() + "/image/" + entry.Image,
		"publish_level": entry.PublishLevel,
		"user": Response{
			"id":   user.Id,
			"name": user.Name,
			"icon": baseUrl.String() + "/icon/" + user.Icon,
		},
	})
}

func timelineHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	latestEntryId, err := strconv.Atoi(r.FormValue("latest_entry"))
	if err != nil {
		latestEntryId = 0
	}

	timeoutMessage := make(chan bool)
	entriesMessage := make(chan []Response)

	go func() {
		time.Sleep(time.Second * timeout)
		timeoutMessage <- true
	}()

	go func() {
		for {
			var (
				rows *sql.Rows
				err  error
			)
			if 0 < latestEntryId {
				rows, err = dbConn.Query(
					"SELECT * FROM (SELECT * FROM entries WHERE (user=? OR publish_level=2 OR (publish_level=1 AND user IN (SELECT target FROM follow_map WHERE user=?))) AND id > ? ORDER BY id LIMIT 30) AS e ORDER BY e.id DESC",
					user.Id, user.Id, latestEntryId,
				)
			} else {
				rows, err = dbConn.Query(
					"SELECT * FROM entries WHERE (user=? OR publish_level=2 OR (publish_level=1 AND user IN (SELECT target FROM follow_map WHERE user=?))) ORDER BY id DESC LIMIT 30",
					user.Id, user.Id,
				)
			}
			if err != nil {
				serverError(w, err)
				return
			}
			entries := []Entry{}
			for rows.Next() {
				entry := Entry{}
				rows.Scan(&entry.Id, &entry.User, &entry.Image, &entry.PublishLevel, &entry.CreatedAt)
				entries = append(entries, entry)
			}
			rows.Close()
			if 0 < len(entries) {
				res := []Response{}
				for _, entry := range entries {
					user := User{}
					err = dbConn.QueryRow(
						"SELECT * FROM users WHERE id = ?", entry.User,
					).Scan(
						&user.Id, &user.Name, &user.Apikey, &user.Icon,
					)
					if err != nil {
						serverError(w, err)
						return
					}
					res = append(res, Response{
						"id":            entry.Id,
						"image":         baseUrl.String() + "/image/" + entry.Image,
						"publish_level": entry.PublishLevel,
						"user": Response{
							"id":   user.Id,
							"name": user.Name,
							"icon": baseUrl.String() + "/icon/" + user.Icon,
						},
					})
				}
				latestEntryId = entries[0].Id
				entriesMessage <- res
				return
			}
			time.Sleep(time.Second * interval)
		}
	}()

	select {
	case entries := <-entriesMessage:
		renderJsonNoCache(w, Response{
			"latest_entry": latestEntryId,
			"entries":      entries,
		})
		return
	case <-timeoutMessage:
		renderJsonNoCache(w, Response{
			"latest_entry": latestEntryId,
			"entries":      []Entry{},
		})
		return
	}
}

func iconHandler(w http.ResponseWriter, r *http.Request) {
	prepareHandler(w, r)

	vars := mux.Vars(r)
	icon := vars["icon"]

	if _, err := os.Stat(config.Datadir + "/icon/" + icon + ".png"); os.IsNotExist(err) {
		notFound(w)
		return
	}

	size := r.FormValue("size")
	if size == "" {
		size = "s"
	}

	var width int
	var height int
	if size == "s" {
		width = iconS
	} else if size == "m" {
		width = iconM
	} else if size == "l" {
		width = iconL
	} else {
		width = iconS
	}
	height = width

	data, err := convert(config.Datadir+"/icon/"+icon+".png", "png", width, height)
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(data)
}

func imageHandler(w http.ResponseWriter, r *http.Request) {
	prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}

	vars := mux.Vars(r)
	image := vars["image"]

	entry := Entry{}
	err = dbConn.QueryRow(
		"SELECT * FROM entries WHERE image = ?", image,
	).Scan(
		&entry.Id, &entry.User, &entry.Image, &entry.PublishLevel, &entry.CreatedAt,
	)
	if err == sql.ErrNoRows {
		notFound(w)
		return
	} else if err != nil {
		serverError(w, err)
		return
	}

	if entry.PublishLevel == 0 {
		// publish_level == 0 はentryの所有者しか見えない
		if user != nil && entry.User == user.Id {
			// ok
		} else {
			notFound(w)
			return
		}
	} else if entry.PublishLevel == 1 {
		// publish_level == 1 はentryの所有者かfollowerしか見えない
		if user != nil && entry.User == user.Id {
			// ok
		} else if user != nil {
			followMap := FollowMap{}
			err = dbConn.QueryRow(
				"SELECT user, target, created_at FROM follow_map WHERE user = ? AND target = ?",
				user.Id, entry.User,
			).Scan(
				&followMap.User, &followMap.Target, &followMap.CreatedAt,
			)
			if err == sql.ErrNoRows {
				notFound(w)
				return
			} else if err != nil {
				serverError(w, err)
				return
			}
		} else {
			notFound(w)
			return
		}
	}

	size := r.FormValue("size")
	if size == "" {
		size = "l"
	}

	var width, height int
	if size == "s" {
		width = imageS
	} else if size == "m" {
		width = imageM
	} else if size == "l" {
		width = imageL
	} else {
		width = imageL
	}
	height = width

	var data []byte
	if 0 <= width {
		path, err := cropSquare(config.Datadir+"/image/"+image+".jpg", "jpg")
		defer os.Remove(path)
		if err != nil {
			serverError(w, err)
			return
		}
		b, err := convert(path, "jpg", width, height)
		if err != nil {
			serverError(w, err)
			return
		}
		data = b
	} else {
		b, err := ioutil.ReadFile(config.Datadir + "/image/" + image + ".jpg")
		if err != nil {
			serverError(w, err)
			return
		}
		data = b
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(data)
}

func convertImage(orig string, target string, w int, h int) {
	cmd := exec.Command("convert", "-quality", "70", "-define", fmt.Sprintf("jpeg:size=%dx%d", w, h), "-geometry", fmt.Sprintf("%dx%d", w, h), orig, target)
	if err := cmd.Run(); err != nil {
		panic(err.Error())
	}
}

func makeImageThumbnails(dir string, name string) {
	m := regexp.MustCompile("^([0-9a-f]+)(_(?:s|m|l))?.jpg$").FindStringSubmatch(name)
	if m == nil {
		return
	}
	if m[2] != "" {
		return
	}

	hash := m[1]
	sizes := []string{"s", "m"}
	for _, size := range sizes {
		target := fmt.Sprintf("%s/%s_%s.jpg", dir, hash, size)
		if FileExists(target) {
			log.Printf("%s exists", target)
			continue
		}
		orig := fmt.Sprintf("%s/%s", dir, name)
		square, err := cropSquare(orig, "jpg")
		if err != nil {
			panic(err.Error())
		}
		switch size {
		case "s":
			log.Printf("making size %s: %s", size, target)
			convertImage(square, target, imageS, imageS)
		case "m":
			log.Printf("making size %s: %s", size, target)
			convertImage(square, target, imageM, imageM)
		}
	}
}

func makeIconThumbnails(dir string, name string) {
	m := regexp.MustCompile("^([0-9a-z]+)(_(?:s|m|l))?.png$").FindStringSubmatch(name)
	if m == nil {
		return
	}
	if m[2] != "" {
		return
	}

	hash := m[1]
	sizes := []string{"s", "m", "l"}
	for _, size := range sizes {
		target := fmt.Sprintf("%s/%s_%s.png", dir, hash, size)
		if FileExists(target) {
			log.Printf("%s exists", target)
			continue
		}
		orig := fmt.Sprintf("%s/%s", dir, name)
		switch size {
		case "s":
			log.Printf("making size %s: %s", size, target)
			convertImage(orig, target, iconS, iconS)
		case "m":
			log.Printf("making size %s: %s", size, target)
			convertImage(orig, target, iconM, iconM)
		case "l":
			log.Printf("making size %s: %s", size, target)
			convertImage(orig, target, iconL, iconL)
		}
	}
}

func FileExists(name string) bool {
	_, err := os.Stat(name)
	return !os.IsNotExist(err)
}

func deleteEntryHandler(w http.ResponseWriter, r *http.Request) {
	prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	vars := mux.Vars(r)
	id := vars["id"]
	method := r.FormValue("__method")

	entry := Entry{}
	err = dbConn.QueryRow(
		"SELECT * FROM entries WHERE id = ?", id,
	).Scan(
		&entry.Id, &entry.User, &entry.Image, &entry.PublishLevel, &entry.CreatedAt,
	)
	if err == sql.ErrNoRows {
		notFound(w)
		return
	} else if err != nil {
		serverError(w, err)
		return
	}

	if user.Id != entry.User || method != "DELETE" {
		badRequest(w)
		return
	}

	_, err = dbConn.Exec("DELETE FROM entries WHERE id = ?", entry.Id)
	if err != nil {
		serverError(w, err)
		return
	}

	renderJson(w, Response{"ok": true})
}

func getFollowing(w http.ResponseWriter, user *User, baseUrl *url.URL) {
	rows, err := dbConn.Query(
		"SELECT users.* FROM follow_map JOIN users ON (follow_map.target = users.id) WHERE follow_map.user = ? ORDER BY follow_map.created_at DESC",
		user.Id,
	)
	if err != nil {
		serverError(w, err)
		return
	}
	res := []Response{}
	for rows.Next() {
		u := User{}
		rows.Scan(&u.Id, &u.Name, &u.Apikey, &u.Icon)
		res = append(res, Response{
			"id":   u.Id,
			"name": u.Name,
			"icon": baseUrl.String() + "/icon/" + u.Icon,
		})
	}
	rows.Close()

	renderJsonNoCache(w, Response{"users": res})
}

func followingHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	getFollowing(w, user, baseUrl)
}

func followHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	if err := r.ParseForm(); err != nil {
		serverError(w, err)
		return
	}

	for _, targetStr := range r.Form["target"] {
		target, _ := strconv.Atoi(targetStr)
		if user.Id == target {
			continue
		}
		_, err := dbConn.Exec(
			"INSERT IGNORE INTO follow_map (user, target, created_at) VALUES (?, ?, NOW())",
			user.Id, target,
		)
		if err != nil {
			serverError(w, err)
			return
		}
	}

	getFollowing(w, user, baseUrl)
}

func unfollowHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	if err := r.ParseForm(); err != nil {
		serverError(w, err)
	}

	for _, targetStr := range r.Form["target"] {
		target, _ := strconv.Atoi(targetStr)
		if user.Id == target {
			continue
		}
		_, err := dbConn.Exec(
			"DELETE FROM follow_map WHERE user = ? AND target = ?",
			user.Id, target,
		)
		if err != nil {
			serverError(w, err)
			return
		}
	}

	getFollowing(w, user, baseUrl)
}

func updateIconHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	uploadFile, handler, err := r.FormFile("image")
	if err != nil {
		serverError(w, err)
		return
	}
	if handler == nil {
		badRequest(w)
		return
	}

	contentType := handler.Header.Get("Content-Type")
	if !regexp.MustCompile("^image/(jpe?g|png)$").MatchString(contentType) {
		badRequest(w)
		return
	}

	data, err := ioutil.ReadAll(uploadFile)
	if err != nil {
		serverError(w, err)
		return
	}

	f, err := ioutil.TempFile(tmpDir, "isucon")
	defer os.Remove(f.Name())
	if err != nil {
		serverError(w, err)
		return
	}

	err = ioutil.WriteFile(f.Name(), data, 0666)
	if err != nil {
		serverError(w, err)
		return
	}

	path, err := cropSquare(f.Name(), "png")
	defer os.Remove(path)
	if err != nil {
		serverError(w, err)
		return
	}

	data, err = ioutil.ReadFile(path)
	if err != nil {
		serverError(w, err)
		return
	}

	iconId := sha256Hex(uuid.NewUUID())
	err = ioutil.WriteFile(config.Datadir+"/icon/"+iconId+".png", data, 0666)
	if err != nil {
		serverError(w, err)
		return
	}

	_, err = dbConn.Exec(
		"UPDATE users SET icon = ? WHERE id = ?",
		iconId, user.Id,
	)
	if err != nil {
		serverError(w, err)
		return
	}

	renderJson(w, Response{"icon": baseUrl.String() + "/icon/" + iconId})
}
