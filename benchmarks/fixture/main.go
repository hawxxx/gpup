// This synthetic endpoint is only for integration tests. It is not an inference engine.
package main

import (
 "flag"
 "fmt"
 "net/http"
 "time"
)

func main(){
 listen:=flag.String("listen","127.0.0.1:17332","Listen address")
 delay:=flag.Duration("delay",10*time.Millisecond,"Delay between synthetic content chunks")
 flag.Parse()
 mux:=http.NewServeMux()
 mux.HandleFunc("/v1/models",func(w http.ResponseWriter,r *http.Request){w.Header().Set("Content-Type","application/json");fmt.Fprint(w,`{"data":[{"id":"gpup-test-fixture"}]}`)})
 mux.HandleFunc("/v1/chat/completions",func(w http.ResponseWriter,r *http.Request){
  w.Header().Set("Content-Type","text/event-stream")
  for i:=0;i<4;i++ {select{case <-r.Context().Done():return;case <-time.After(*delay):};fmt.Fprint(w,"data: {\"choices\":[{\"delta\":{\"content\":\"test \"}}]}\n\n");w.(http.Flusher).Flush()}
  fmt.Fprint(w,"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n")
 })
 if err:=http.ListenAndServe(*listen,mux);err!=nil {panic(err)}
}
