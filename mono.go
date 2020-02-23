// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	_ "bufio"
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"github.com/nfnt/resize"
	"html/template"
	"image"
	"image/jpeg"
	"io"
	_ "log"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strconv"
	_ "strings"
	"time"
)

var (
	MonoConfig Config
	MonoDB     *sql.DB
)

func main() {
	initConfig()
	initDb()
	initDir()

	r := mux.NewRouter()
	r.PathPrefix("/assets/").Handler(http.StripPrefix("/assets/", http.FileServer(http.Dir("./assets"))))

	r.HandleFunc("/api/mono/day/", monoDayHandler).Methods("GET")
	r.HandleFunc("/api/mono/day/{knotday}", monoDayKnotdayHandler).Methods("GET")

	r.HandleFunc("/api/mono/one/", monoOnePostHandler).Methods("POST")
	r.HandleFunc("/api/mono/one/{id}", monoOneGetHandler).Methods("GET")
	r.HandleFunc("/api/mono/one/{id}", monoOnePutHandler).Methods("PUT")

	r.HandleFunc("/api/mono/header", monoHeaderHandler).Methods("GET")
	r.HandleFunc("/api/mono/footer", monoFooterHandler).Methods("GET")

	r.HandleFunc("/api/huau", loginHandler)
	r.HandleFunc("/api/nothing", logoutHandler)
	r.HandleFunc("/api/me", isloginHandler)

	r.HandleFunc("/upload", uploadHandler).Methods("POST")

	r.NotFoundHandler = http.HandlerFunc(mainHandler)
	http.ListenAndServe(":"+MonoConfig.Port, r)

	defer MonoDB.Close()
}

/***************
   Handler
***************/

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	filename := r.FormValue("filename") + ".jpg"
	file, _, _ := r.FormFile("file")
	defer file.Close()

	for {
		if _, err := os.Stat("./assets/images/original/" + filename); os.IsNotExist(err) {
			break
		}
		reg := regexp.MustCompile("(?:_([0-9]+))?\\..*?$")
		num := reg.FindStringSubmatch(filename)
		filename = reg.ReplaceAllString(filename, "")
		if len(num) == 0 {
			filename = filename + "_1.jpg"
		} else {
			x, _ := strconv.Atoi(num[1])
			filename += fmt.Sprintf("_%d.jpg", (x + 1))
		}
	}

	trace("** final filename: ", filename)

	f, err := os.OpenFile("./assets/images/original/"+filename, os.O_WRONLY|os.O_CREATE, 0666)
	trace("original : ./assets/images/original/" + filename)
	if err != nil {
		return
	}

	defer f.Close()
	io.Copy(f, file)

	//resize
	//ori, err := jpeg.Decode(f)
	f2, err := os.Open("./assets/images/original/" + filename)
	if err != nil {
		trace(err)
	}
	defer f2.Close()

	ori, _, err := image.Decode(f2)
	if err != nil {
		trace(err)
	}
	resized := resize.Thumbnail(530, 530, ori, resize.Lanczos3)

	out, err := os.Create("./assets/images/" + filename)
	trace("** thubnail : ./assets/images/" + filename)
	if err != nil {
		trace(err)
	}
	defer out.Close()
	jpeg.Encode(out, resized, nil)

	added := fmt.Sprintf("@img %s@", filename)

	json.NewEncoder(w).Encode(MonoUpload{Filename: filename, Added: added})
}

func monoHeaderHandler(w http.ResponseWriter, r *http.Request) {
	piece := headerMapper()
	content := parse(piece)

	json.NewEncoder(w).Encode(MonoHeader{Content: content})
}

func monoFooterHandler(w http.ResponseWriter, r *http.Request) {
	piece := footerMapper()
	content := parse(piece)

	json.NewEncoder(w).Encode(MonoFooter{Content: content})
}

func mainHandler(w http.ResponseWriter, r *http.Request) {
	trace("## request => ", r.URL.Path)

	tmpl := template.Must(template.ParseFiles("mono.html"))

	//mono := Mono{Id: id, Content: content}
	tmpl.Execute(w, "main")
}

func monoDayHandler(w http.ResponseWriter, r *http.Request) {
	knotday := lastKnotdayMapper()
	monoDayCommonHandler(w, r, knotday)
}

func monoDayKnotdayHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	knotday := vars["knotday"]
	knotday = oTozero(knotday)
	trace("* knotday : ", knotday)

	monoDayCommonHandler(w, r, knotday)
}

func monoDayCommonHandler(w http.ResponseWriter, r *http.Request, knotday string) {
	trace("param : %s", knotday)

	var monos []MonoPiece
	monos = getMonoDayMapper(knotday)
	move := getMonoDayMovingMapper(knotday)

	knotday = zeroToO(knotday)

	json.NewEncoder(w).Encode(MonoContext{Monos: monos, MonoMove: move, Knotday: knotday})

}

func monoOneGetHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	mono := getMonoOneMapper(id)
	knots := getKnotMapper(id)
	mono.Knots = knots

	json.NewEncoder(w).Encode(mono)

	trace("get: ")
	trace(mono)
}

func monoOnePutHandler(w http.ResponseWriter, r *http.Request) {
	if isloginMapper(w, r) == false {
		trace("login checking fail.")
		return
	}

	var mm MonoPiece
	err := json.NewDecoder(r.Body).Decode(&mm)
	if err != nil {
		trace(err)
	}

	mm.Realday = oTozero(mm.Realday)
	mm.Knotday = oTozero(mm.Knotday)

	x := mm.Realday
	mm.Knotday = x[len(x)-4:]

	putMonoOneMapper(&mm)

	deleteKnotMapper(mm.Id)
	sp := regexp.MustCompile("\\s*\\.\\s*")
	knots := sp.Split(mm.KnotFrom, -1)
	for _, knot := range knots {
		postKnotMapper(mm.Id, knot)
	}

}

func monoOnePostHandler(w http.ResponseWriter, r *http.Request) {
	if isloginMapper(w, r) == false {
		trace("login checking fail.")
		return
	}

	var mm MonoPiece
	err := json.NewDecoder(r.Body).Decode(&mm)
	if err != nil {
		trace(err)
	}

	if mm.Realday == "" {
		mm.Realday = time.Now().Format("20060102")
	}

	trace("post: ")
	trace(mm)

	x := mm.Realday
	mm.Knotday = x[len(x)-4:]

	id := postMonoOneMapper(&mm)
	mm.Id = strconv.Itoa(id)

	sp := regexp.MustCompile("\\s*\\.\\s*")
	knots := sp.Split(mm.KnotFrom, -1)
	for _, knot := range knots {
		postKnotMapper(mm.Id, knot)
	}

	json.NewEncoder(w).Encode(mm)
}

func loginHandler(w http.ResponseWriter, r *http.Request) {

	queryValues := r.URL.Query()
	p_userid := queryValues.Get("Userid")
	p_passwd := queryValues.Get("Passwd")

	//vars := mux.Vars(r)
	//p_userid := vars["Userid"]
	//p_passwd := vars["Passwd"]

	var passwd string
	err := MonoDB.QueryRow("SELECT passwd FROM mono_user WHERE userid = ?", p_userid).Scan(&passwd)
	if err != nil {
		trace("db")
		trace(err)
	}

	h := sha1.New()
	io.WriteString(h, p_passwd)
	ps := fmt.Sprintf("%x", h.Sum(nil))

	if ps == passwd {
		trace("valid password.")
		expiration := time.Now().Add(365 * 24 * time.Hour)

		c := sha1.New()
		io.WriteString(c, fmt.Sprint(time.Now().Unix()))
		io.WriteString(c, passwd)
		c_str := fmt.Sprintf("%x", c.Sum(nil))

		cookie := http.Cookie{Name: "mono_cookie", Value: c_str, Path: "/", Expires: expiration, MaxAge: 86400}
		userid := http.Cookie{Name: "mono_userid", Value: p_userid, Path: "/", Expires: expiration, MaxAge: 86400}
		http.SetCookie(w, &cookie)
		http.SetCookie(w, &userid)

		stmt, err := MonoDB.Prepare("update mono_user set session = ? where userid = ?")
		res, err := stmt.Exec(c_str, p_userid)

		if err != nil {
			trace(err.Error())
		}
		trace(res)
		trace("set cookie!!!")

		json.NewEncoder(w).Encode(MonoSession{Islogin: true})

	} else {
		json.NewEncoder(w).Encode(MonoSession{Islogin: false})
	}
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	cookie := http.Cookie{Name: "mono_cookie", Value: "", Path: "/", Expires: time.Time{}, MaxAge: 0}
	userid := http.Cookie{Name: "mono_userid", Value: "", Path: "/", Expires: time.Time{}, MaxAge: 0}
	http.SetCookie(w, &cookie)
	http.SetCookie(w, &userid)
}

func isloginHandler(w http.ResponseWriter, r *http.Request) {
	isme := isloginMapper(w, r)
	c_userid, _ := r.Cookie("mono_userid")
	userid := ""
	if c_userid != nil {
		userid = c_userid.Value

	}
	json.NewEncoder(w).Encode(MonoSession{Islogin: isme, Userid: userid})
}

/***************
   Mapper
***************/
func headerMapper() MonoPiece {
	var piece MonoPiece
	err := MonoDB.QueryRow("SELECT id, content FROM mono_piece WHERE length(knot)>0 and knot=(SELECT knot_header FROM mono_extra)").Scan(&piece.Id, &piece.Content)
	if err != nil {
		trace(err.Error())
	}

	return piece
}

func footerMapper() MonoPiece {
	var piece MonoPiece
	err := MonoDB.QueryRow("SELECT id, content FROM mono_piece WHERE length(knot)>0 and knot=(SELECT knot_footer FROM mono_extra)").Scan(&piece.Id, &piece.Content)
	if err != nil {
		trace(err.Error())
	}

	return piece
}

func lastKnotdayMapper() string {
	knotday := ""
	today := time.Now().Format("20060102")
	err := MonoDB.QueryRow("SELECT knotday FROM mono_piece WHERE realday <= ? ORDER BY realday desc limit 1", today).Scan(&knotday)

	if err != nil {
		trace(err.Error())
	}

	if knotday == "" {
		knotday = today[4:]
	}

	return knotday
}

func getMonoDayMapper(knot string) []MonoPiece {
	rows, err := MonoDB.Query("SELECT id, knot, content, realday, knotday, changed FROM mono_piece WHERE knotday = ? ORDER BY realday desc", knot)
	if err != nil {
		trace(err.Error())
	}

	var mono []MonoPiece

	for rows.Next() {
		var mm MonoPiece
		err := rows.Scan(&mm.Id, &mm.Knot, &mm.Content, &mm.Realday, &mm.Knotday, &mm.Changed)
		if err != nil {
			trace(err.Error())
			break
		}

		mm.ContentParsed = parse(mm)
		mm.Realday = zeroToO(mm.Realday)
		mm.Knotday = zeroToO(mm.Knotday)
		mm.Changed = zeroToO(mm.Changed)

		mono = append(mono, mm)
	}

	return mono
}

func getMonoDayMovingMapper(knot string) MonoMove {
	var mv MonoMove
	err := MonoDB.QueryRow("SELECT ifnull(prev, (SELECT ifnull(max(knotday), '') FROM mono_piece)) a, ifnull(next, (SELECT ifnull(min(knotday), '') FROM mono_piece)) b FROM ( SELECT (SELECT knotday FROM mono_piece WHERE knotday < ? ORDER BY knotday DESC LIMIT 1) prev, (SELECT knotday FROM mono_piece WHERE knotday > ? ORDER BY knotday LIMIT 1) next ) x", knot, knot).Scan(&mv.PrevKnot, &mv.NextKnot)
	if err != nil {
		trace(err.Error())
	}

	return mv

}

func getMonoOneMapper(id string) MonoPiece {
	var mm MonoPiece
	err := MonoDB.QueryRow("SELECT id, content, knot, realday, knotday, changed FROM mono_piece WHERE id = ?", id).Scan(&mm.Id, &mm.Content, &mm.Knot, &mm.Realday, &mm.Knotday, &mm.Changed)
	if err != nil {
		trace(err.Error())
	}

	mm.ContentParsed = parse(mm)
	mm.Realday = zeroToO(mm.Realday)
	mm.Knotday = zeroToO(mm.Knotday)
	mm.Changed = zeroToO(mm.Changed)

	return mm
}

func postMonoOneMapper(mm *MonoPiece) int {
	trace(mm)
	stmt, err := MonoDB.Prepare("INSERT INTO mono_piece (id, content, knot, realday, knotday, changed) VALUES (null, ?, ?, ?, ?, datetime('now','localtime'))")
	res, err := stmt.Exec(mm.Content, mm.Knot, mm.Realday, mm.Knotday)

	if err != nil {
		trace(err.Error())
	}
	trace(res)

	id, err := res.LastInsertId()

	return int(id)
}

func putMonoOneMapper(mm *MonoPiece) {
	trace(mm)
	stmt, err := MonoDB.Prepare("UPDATE mono_piece SET content = ?, knot = ?, realday = ?, knotday = ?, changed = datetime('now','localtime') WHERE id = ?")
	res, err := stmt.Exec(mm.Content, mm.Knot, mm.Realday, mm.Knotday, mm.Id)

	if err != nil {
		trace(err.Error())
	}
	trace(res)
}

func getKnotMapper(id string) []MonoKnot {
	rows, err := MonoDB.Query("SELECT K.knotid id, P.knot FROM mono_knot K INNER JOIN mono_piece P ON (K.knotid = P.id) WHERE K.pieceid = ?", id)
	if err != nil {
		trace(err.Error())
	}

	var knots []MonoKnot

	for rows.Next() {
		var mm MonoKnot
		err := rows.Scan(&mm.Id, &mm.Knot)
		if err != nil {
			trace(err.Error())
			break
		}

		knots = append(knots, mm)
	}

	return knots
}

func postKnotMapper(id string, knot string) {
	trace(id, knot)
	if knot == "" {
		return
	}

	stmt, err := MonoDB.Prepare("INSERT INTO mono_knot SELECT id, ? FROM mono_piece WHERE knot = ?")
	res, err := stmt.Exec(id, knot)

	if err != nil {
		trace(err.Error())
	}
	trace(res)
}

func deleteKnotMapper(id string) {
	stmt, err := MonoDB.Prepare("DELETE FROM mono_knot WHERE pieceid = ?")
	res, err := stmt.Exec(id)
	if err != nil {
		trace(err.Error())
	}
	trace(res)
}

func getPiecesMapper(id string) []MonoPiece {
	rows, err := MonoDB.Query("SELECT P.id, P.knot, P.realday, P.changed, P.content FROM mono_piece P INNER JOIN mono_knot K ON (K.pieceid = P.id) WHERE K.knotid = ?", id)
	if err != nil {
		trace(err.Error())
	}

	var pieces []MonoPiece

	for rows.Next() {
		var mm MonoPiece
		err := rows.Scan(&mm.Id, &mm.Knot, &mm.Realday, &mm.Changed, &mm.Content)
		if err != nil {
			trace(err.Error())
			break
		}
		mm.Realday = zeroToO(mm.Realday)
		mm.Changed = zeroToO(mm.Changed)

		pieces = append(pieces, mm)
	}

	return pieces
}

func isloginMapper(w http.ResponseWriter, r *http.Request) bool {
	c_cookie, _ := r.Cookie("mono_cookie")
	c_userid, _ := r.Cookie("mono_userid")

	if (c_cookie == nil || c_cookie.Value == "") || (c_userid == nil || c_userid.Value == "") {
		return false
	}

	var session string
	err := MonoDB.QueryRow("SELECT session FROM mono_user WHERE userid = ?", c_userid.Value).Scan(&session)
	if err != nil {
		trace("db")
		trace(err)
	}

	return c_cookie.Value == session
}

func initConfig() {
	trace("## init config.")
	config := Config{}

	f, _ := os.Open("./mono.conf")
	defer f.Close()

	d := json.NewDecoder(f)
	err := d.Decode(&config)
	if err != nil {
		panic(err)
	}

	trace("* config => ", config)
	MonoConfig = config
}

func initDb() {
	trace("## initDb()")
	db, err := sql.Open("sqlite3", MonoConfig.DbFile)
	checkErr(err)

	err = db.Ping()
	if err != nil {
		trace("Error on opening database connection: %s", err.Error())
	} else {
		trace("* db checking ok.")
	}

	MonoDB = db
}

func initDir() {
	trace("##", "nana")
	if _, err := os.Stat("assets/images"); os.IsNotExist(err) {
		os.Mkdir("assets/images", 0755)
	}
	if _, err := os.Stat("assets/images/original"); os.IsNotExist(err) {
		os.Mkdir("assets/images/original", 0755)
	}
}

func checkErr(err error) {
	if err != nil {
		panic(err)
	}
}

/***************
   Structure
***************/

type Config struct {
	Port   string
	DbFile string
}

type MonoPiece struct {
	Id            string
	Content       string
	ContentParsed string
	Knot          string
	Realday       string
	Knotday       string
	Changed       string
	KnotFrom      string
	Knots         []MonoKnot
}

type MonoKnot struct {
	Id   string
	Knot string
}

type MonoMove struct {
	PrevKnot string
	NextKnot string
}

type MonoContext struct {
	Monos    []MonoPiece
	MonoMove MonoMove
	Knotday  string
}

type MonoUpload struct {
	Filename string
	Added    string
}

type MonoSession struct {
	Islogin bool
	Userid  string
}

type MonoHeader struct {
	Content string
}

type MonoFooter struct {
	Content string
}

/***************
   Parser
***************/

func parse(mm MonoPiece) string {
	//r1 := regexp.MustCompile("[0-9a-z]")
	//text = r1.ReplaceAllString(text, "*")
	var text = mm.Content

	text = regexp.MustCompile("@img (.*?)@").ReplaceAllString(text, "<img src=\"/assets/images/$1\" />")
	text = regexp.MustCompile("@sound ([^\\s]+) (.*?)@").ReplaceAllString(text, "<a href=\"#\" onclick=\"musicSelect('/assets/sounds/$1', '$2');\">$2</a>")
	text = regexp.MustCompile("@pieces@").ReplaceAllString(text, parse_pieces(mm.Id))
	text = regexp.MustCompile("\n").ReplaceAllString(text, "<br />")

	return text
}

func parse_pieces(id string) string {
	var pieces []MonoPiece
	pieces = getPiecesMapper(id)
	rs := ""
	for p := range pieces {
		content := regexp.MustCompile("\n.*$").ReplaceAllString(pieces[p].Content, "$1")
		pieces[p].Content = content
		content = parse(pieces[p])

		if pieces[p].Knot == "" {
			rs += fmt.Sprintf("%s  <router-link :to=\"{name: 'monoOneIt', params:{id: %s}}\">%s</router-link>\n", content, pieces[p].Id, pieces[p].Changed)
		} else {
			rs += fmt.Sprintf("%s  <router-link :to=\"{name: 'monoOneIt', params:{id: %s}}\">%s</router-link>\n", content, pieces[p].Id, pieces[p].Knot)
		}
	}

	return rs
}

//func parse_pce() string {
//}

/***************
   Utils
***************/

func zeroToO(str string) string {
	r1 := regexp.MustCompile("[^0-9o]")
	str = r1.ReplaceAllString(str, "")

	r2 := regexp.MustCompile("0")
	str = r2.ReplaceAllString(str, "o")

	return str
}

func oTozero(str string) string {
	r := regexp.MustCompile("o")
	str = r.ReplaceAllString(str, "0")

	return str
}

func trace(msg ...interface{}) {
	pc := make([]uintptr, 10) // at least 1 entry needed
	runtime.Callers(2, pc)
	f := runtime.FuncForPC(pc[0])
	_, line := f.FileLine(pc[0])
	fmt.Printf("mono::%5d::%-30s - %s\n", line, f.Name(), msg)
}
