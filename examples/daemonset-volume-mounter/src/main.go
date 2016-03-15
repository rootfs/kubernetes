package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/unrolled/render"
)

type Mounter struct {
	Name string
}

func InfoHandler(res http.ResponseWriter, req *http.Request) {
	var m Mounter
	m.Name = "glusterfs"

	r := render.New(render.Options{})
	r.JSON(res, 200, m)

}

func MountHandler(res http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	t := vars["type"]
	src := vars["src"]
	dest := vars["dest"]
	opt := vars["opt"]
	println("mount", "-t", t, src, dest, opt)

	r := render.New(render.Options{})
	cmd := exec.Command("mount", "-t", t, src, dest, "-o", opt)
	output, err := cmd.CombinedOutput()
	st := 200
	if err != nil {
		st = 400
		fmt.Printf("%v", string(output))
	}

	r.JSON(res, st, string(output))

}

func main() {
	var port = ":" + os.Getenv("SERVICE_PORT")
	if port == ":" {
		port = ":3000"
	}

	r := mux.NewRouter()

	// define RESTful handlers
	r.Path("/info").Methods("GET").HandlerFunc(InfoHandler)
	r.Path("/mount/{type}/{src}/{dest}/{opt}").Methods("GET").HandlerFunc(MountHandler)

	n := negroni.New(negroni.NewLogger())

	n.UseHandler(r)

	n.Run(port)
}
