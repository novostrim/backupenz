/*
   (c) 2015 Novostrim, OOO. http://www.eonza.org
   License: MIT
*/

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/vharitonsky/iniflags"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	//	"reflect"
	"strconv"
	"strings"
)

const (
	VERSION = `1.0.1`
)

func IsEmpty(in string) bool {
	return len(in) == 0
}

type DwnProgress struct {
	io.Reader
	filename string
	total    int64
	current  int64
}

func (mc *DwnProgress) Read(p []byte) (int, error) {
	n, err := mc.Reader.Read(p)
	mc.current += int64(n)
	if err == nil {
		var percent int64
		if mc.total != 0 {
			percent = 100 * mc.current / mc.total
		}
		fmt.Print(`Downloading `, mc.filename, `: `, percent, "%\r")
	}

	return n, err
}

type appFlags struct {
	url     string
	login   string
	pass    string
	storage string
	log     string
	db      bool
	all     bool
}

var (
	flags      appFlags
	enzurl     *url.URL
	storage    string
	client     *http.Client
	downloaded int
	dirs       map[string]bool
	fullList   map[string]bool
)

func FlagParam(dest interface{}, short string,
	long string, comment string) {
	switch dest.(type) {
	case *string:
		flag.StringVar(dest.(*string), long, ``, comment)
		flag.StringVar(dest.(*string), short, ``, comment+` (shorthand)`)
	case *bool:
		flag.BoolVar(dest.(*bool), long, false, comment)
		flag.BoolVar(dest.(*bool), short, false, comment+` (shorthand)`)
	}
}

func init() {
	FlagParam(&flags.url, `e`, `Eonza`, `Eonza URL`)
	FlagParam(&flags.login, `u`, `User`, `Login`)
	FlagParam(&flags.pass, `p`, `Psw`, `Password`)
	FlagParam(&flags.storage, `s`, `Storage`, `Local storage path`)
	FlagParam(&flags.log, `l`, `Log`, `Log file`)
	FlagParam(&flags.all, "m", `Mirror`, `Mirror synchronization`)
	FlagParam(&flags.db, "db", `Dbackup`, `Backup database`)
}

func getapi(in string) string {
	return enzurl.String() + `api/` + in
}

func connect(client *http.Client) error {
	v := url.Values{`login`: {flags.login}, `psw`: {flags.pass}}
	resp, err := client.PostForm(getapi(`login`), v)

	if err != nil {
		fmt.Println(err)
		panic(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatalf("ERROR! HTTP status code %v is wrong", resp.StatusCode)
	}
	in, err := ioutil.ReadAll(resp.Body)
	// st := string(in)
	var jret interface{}
	json.Unmarshal(in, &jret)
	answer := jret.(map[string]interface{})
	if !answer[`success`].(bool) {
		return fmt.Errorf(`You have specified the wrong login or the password`)
	}
	//	fmt.Println( answer )
	return nil
}

func getmaxid() (id int) {
	if flags.all {
		return 0
	}
	d, err := os.Open(storage)
	if err != nil {
		return
	}
	defer d.Close()
	fid, err := d.Readdir(0)
	if err != nil {
		log.Fatalln(err)
	}
	for _, fi := range fid {
		if fi.Mode().IsDir() {
			dtbl, err := os.Open(filepath.Join(storage, fi.Name()))
			if err != nil {
				log.Fatalln(err)
			}
			defer dtbl.Close()
			fidtbl, err := dtbl.Readdir(0)
			if err != nil {
				log.Fatalln(err)
			}
			var maxdir int
			for _, fitbl := range fidtbl {
				//				fmt.Println( filepath.Join(storage, fi.Name(), fitbl.Name()) )
				if fitbl.IsDir() {
					if curdir, _ := strconv.Atoi(fitbl.Name()); curdir > maxdir {
						maxdir = curdir
					}
				}
			}
			if maxdir > 0 {
				walkfunc := func(path string, f os.FileInfo, err error) error {
					if f.Mode().IsRegular() {
						if curid, _ := strconv.Atoi(f.Name()); curid > id {
							id = curid
						}
					}
					return nil
				}
				filepath.Walk(filepath.Join(storage, fi.Name(),
					strconv.Itoa(maxdir)), walkfunc)
			}
		}
	}
	return id
}

type GetFilesItem struct {
	Id        string
	Folder    string
	Filename  string
	Size      string
	IsPreview string
	IdTable   string
}

type GetFilesRet struct {
	Success bool
	Err     interface{}
	Result  []GetFilesItem
	Temp    string
}

type AnswerRet struct {
	Success bool
	Err     interface{}
	Result  []interface{}
	Temp    string
}

type CreateBackupRet struct {
	Success   bool
	Err       interface{}
	Result    []interface{}
	Filename  string
	BackupUrl string
	Temp      string
}

func downloadfile(gfi *GetFilesItem, filename string, thumb bool) {
	dir := filepath.Dir(filename)
	if _, ok := dirs[dir]; !ok {
		if os.MkdirAll(dir, 0777) != nil {
			log.Fatalln(`Cannot create `, dir)
		}
		dirs[dir] = true
		//		fmt.Println("Created folder: ", dir )
	}
	file, err := os.Create(filename)
	if err != nil {
		log.Fatalln(`Cannot create `, filename)
	}
	defer file.Close()
	var sthumb string
	if thumb {
		sthumb = `&thumb=1`
	}
	httpReq, err := http.NewRequest("GET", getapi(`download?id=`+gfi.Id+sthumb), nil)
	resp, err := client.Do(httpReq)
	if err != nil || resp.StatusCode != 200 {
		log.Fatalln(`Cannot download `, filename)
	}
	defer resp.Body.Close()
	//	fisize, err := strconv.Atoi(resp.Header.Get("Content-Length"))
	fisize, _ := strconv.Atoi(gfi.Size)
	size, err := io.Copy(file, &DwnProgress{resp.Body, gfi.Filename, int64(fisize), 0})
	if int(size) == fisize || thumb {
		log.Println("Download: ", gfi.Filename, `as`, filepath.Join(gfi.IdTable, gfi.Folder, gfi.Id))
		downloaded++
	} else {
		log.Fatalln(`ERROR download `, gfi.Filename)
	}
}

func loadfiles(maxid int) {
	onpage := 100
	for {
		httpReq, err := http.NewRequest("GET", getapi(fmt.Sprint(`getfiles?onlyf=1&op=`,
			onpage, `&from=`, maxid)), nil)
		resp, err := client.Do(httpReq)
		if err != nil {
			log.Fatalln(err)
		}
		defer resp.Body.Close()
		in, err := ioutil.ReadAll(resp.Body)
		var getfiles GetFilesRet
		json.Unmarshal(in, &getfiles)
		//		st := string(in)
		//		fmt.Println(st)
		if !getfiles.Success {
			log.Fatalln(`ERROR: `, getfiles.Err)
		}
		for _, v := range getfiles.Result {
			if folder, _ := strconv.Atoi(v.Folder); folder == 0 {
				log.Fatalln(`Error: API`)
			}
			size, _ := strconv.Atoi(v.Size)
			filename := filepath.Join(storage, v.IdTable, v.Folder, v.Id)
			maxid, _ = strconv.Atoi(v.Id)
			if fi, err := os.Stat(filename); err == nil {
				delete(fullList, filename)
				//				log.Println("File ", filename, ` exists`)
				if int(fi.Size()) == size {
					continue
				}
			}
			downloadfile(&v, filename, false)
			if isprev, _ := strconv.ParseBool(v.IsPreview); isprev {
				prevName := filepath.Join(filepath.Dir(filename), `_`+v.Id)
				delete(fullList, prevName)
				downloadfile(&v, prevName, true)
			}
		}
		if len(getfiles.Result) < onpage {
			break
		}
	}
}

func getfulllist() {
	fullList = make(map[string]bool)

	d, err := os.Open(storage)
	if err != nil {
		return
	}
	defer d.Close()
	fid, err := d.Readdir(0)
	if err != nil {
		log.Fatalln(err)
	}
	for _, fi := range fid {
		if fi.Mode().IsDir() {
			if curdir, _ := strconv.Atoi(fi.Name()); curdir == 0 {
				continue
			}
			dtbl, err := os.Open(filepath.Join(storage, fi.Name()))
			if err != nil {
				log.Fatalln(err)
			}
			defer dtbl.Close()
			walkfunc := func(path string, f os.FileInfo, err error) error {
				if f.Mode().IsRegular() {
					fullList[path] = true
				}
				return nil
			}
			filepath.Walk(filepath.Join(storage, fi.Name()), walkfunc)
		}
	}
}

func createbackup() {
	fmt.Println(`Creating the database backup`)
	httpReq, err := http.NewRequest("GET", getapi(`createbackup`), nil)
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Fatalln(err)
	}
	in, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()

	var createbackup CreateBackupRet
	json.Unmarshal(in, &createbackup)
	if !createbackup.Success {
		log.Fatalln(`ERROR: `, createbackup.Err)
	}

	dir := filepath.Join(storage, `backup`)
	if os.MkdirAll(dir, 0777) != nil {
		log.Fatalln(`Cannot create backup folder`, dir)
	}
	filename := filepath.Join(dir, createbackup.Filename)
	file, err := os.Create(filename)
	if err != nil {
		log.Fatalln(`Cannot create `, filename)
	}
	defer file.Close()
	srcfile := fmt.Sprint(enzurl.Scheme, `://`, enzurl.Host, `/`, strings.TrimLeft(createbackup.BackupUrl, `/`),
		`/`, createbackup.Filename)
	httpReq, _ = http.NewRequest("GET", srcfile, nil)
	resp, err = client.Do(httpReq)
	if err != nil || resp.StatusCode != 200 {
		log.Fatalln(`Cannot download `, srcfile)
	}

	fisize, _ := strconv.Atoi(resp.Header.Get("Content-Length"))
	size, err := io.Copy(file, &DwnProgress{resp.Body, filename, int64(fisize), 0})
	if int(size) == fisize {
		log.Println("Download backup: ", filename)
	} else {
		log.Fatalln(`ERROR download backup`, filename)
	}
	resp.Body.Close()

	v := url.Values{`filename`: {createbackup.Filename}}
	resp, err = client.PostForm(getapi(`delbackup`), v)
	if err != nil {
		log.Fatalln(err)
	}
	defer resp.Body.Close()

	in, err = ioutil.ReadAll(resp.Body)
	var delbackup AnswerRet
	json.Unmarshal(in, &delbackup)
	if !delbackup.Success {
		log.Println(`Cannot delete: `, createbackup.Filename)
	}

}

func main() {
	iniflags.Parse()
	fmt.Println(`Backup Eonza Files v`+VERSION, ` beta, (c) Novostrim OOO, 2015`)
	fmt.Println(`Site: http://www.eonza.org`)
	dirs = make(map[string]bool)
	if !IsEmpty(flags.log) {
		flags.log, _ = filepath.Abs(flags.log)
		f, err := os.OpenFile(flags.log, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			log.Fatalln("Error opening log file: %v", err)
		}
		defer f.Close()
		fmt.Println(`Log-file: `, flags.log)
		log.SetOutput(f)
	}
	log.Println("Start backupenz")
	for IsEmpty(flags.url) {
		fmt.Print("Eonza URL: ")
		fmt.Scanln(&flags.url)
	}

	var err error
	enzurl, err = url.Parse(flags.url)
	if err != nil {
		log.Fatalln("ERROR! Eonza URL is not valid")
	}
	if !strings.HasSuffix(enzurl.Path, `/`) {
		enzurl.Path += `/`
	}
	cookieJar, _ := cookiejar.New(nil)
	client = &http.Client{
		Jar: cookieJar,
	}
	for {
		for IsEmpty(flags.login) {
			fmt.Print("Login: ")
			fmt.Scanln(&flags.login)
		}
		for IsEmpty(flags.pass) {
			fmt.Print("Password: ")
			fmt.Scanln(&flags.pass)
		}
		err := connect(client)
		if err == nil {
			break
		}
		fmt.Println(err)
		flags.login = ``
		flags.pass = ``
	}
	if IsEmpty(flags.storage) {
		flags.storage, _ = os.Getwd()
	}
	storage = filepath.Join(flags.storage, enzurl.Host, filepath.FromSlash(enzurl.Path))
	log.Println("Storage path: ", storage)
	if flags.all {
		getfulllist()
	}
	loadfiles(getmaxid())
	if downloaded == 0 {
		log.Println(`There are no files to download`)
	}
	if flags.all {
		todel := 0
		for key := range fullList {
			if base := filepath.Base(key); strings.HasPrefix(base, `_`) {
				continue
			}
			todel++
		}
		isdel := `Y`
		if IsEmpty(flags.log) && todel > 0 {
			for {
				fmt.Print(todel, ` files have not been found on the server.
Would you like to delete them localy? Yes[Y]/No[N]: `)
				fmt.Scanln(&isdel)
				isdel = strings.ToUpper(isdel)
				if isdel == `Y` || isdel == `N` {
					break
				}
			}
		}
		if isdel == `Y` {
			for key := range fullList {
				if os.Remove(key) == nil {
					log.Println(`Delete:`, key)
				}
			}
		}
	}
	if flags.db {
		createbackup()
	}
	log.Println("backupenz has been successfully finished")
}
