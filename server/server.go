package main

import (
	server "hummerhead87/chat"
	"log"
	"net/http"
	"os"

	"github.com/99designs/gqlgen/handler"
	"github.com/amirhosseinab/sfs"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v2"
	"github.com/rs/cors"
)

const defaultHTTPPort = "4000"
const defaultHTTPSPort = "4001"

var (
	// Media engine
	mEngine webrtc.MediaEngine
	// API object
	api *webrtc.API
)

func init() {
	// Generate pem file for https
	genPem()

	// Create a MediaEngine object to configure the supported codec
	mEngine = webrtc.MediaEngine{}

	// Setup the codecs you want to use.
	// m.RegisterCodec(webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))

	mEngine.RegisterCodec(webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))
	mEngine.RegisterCodec(webrtc.NewRTPH264Codec(webrtc.DefaultPayloadTypeH264, 90000))
	mEngine.RegisterCodec(webrtc.NewRTPVP9Codec(webrtc.DefaultPayloadTypeVP9, 90000))
	mEngine.RegisterCodec(webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 48000))

	// Create the API object with the MediaEngine
	api = webrtc.NewAPI(webrtc.WithMediaEngine(mEngine))
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultHTTPPort
	}

	portHTTPS := os.Getenv("PORT_HTTPS")
	if portHTTPS == "" {
		portHTTPS = defaultHTTPSPort
	}

	resolver, err := server.NewResolver(mEngine, api)
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	mux.Handle(
		"/graphql",
		handler.GraphQL(
			server.NewExecutableSchema(
				server.Config{Resolvers: resolver},
			),
			handler.WebsocketUpgrader(websocket.Upgrader{
				CheckOrigin: func(r *http.Request) bool {
					return true
				},
			}),
		),
	)
	mux.Handle("/playground", handler.Playground("GraphQL", "/graphql"))

	fs := sfs.New(http.Dir("static"), IndexHandler)
	// fs := http.FileServer(http.Dir("static"))
	mux.Handle("/", fs)
	// mux.Handle("/", http.FileServer(http.File("static/index.html")))

	handler := cors.AllowAll().Handler(mux)

	log.Printf("connect to http://localhost:%s/playground for GraphQL playground", port)
	go func() {
		log.Fatal(http.ListenAndServe(":"+port, handler))
	}()
	log.Fatal(http.ListenAndServeTLS(":"+portHTTPS, "cert.pem", "key.pem", handler))

	// http.Handle("/", handler.Playground("GraphQL playground", "/query"))
	// http.Handle("/query", handler.GraphQL(
	// 	server.NewExecutableSchema(
	// 		server.Config{Resolvers: resolver},
	// 	),
	// ))

	// log.Printf("connect to http://localhost:%s/ for GraphQL playground", port)
	// log.Fatal(http.ListenAndServe(":"+port, nil))

	// port := os.Getenv("PORT")
	// if port == "" {
	// 	port = defaultPort
	// }

	// http.Handle("/", handler.Playground("GraphQL playground", "/query"))
	// http.Handle("/query", handler.GraphQL(server.NewExecutableSchema(server.Config{Resolvers: &server.Resolver{}})))

	// log.Printf("connect to http://localhost:%s/ for GraphQL playground", port)
	// log.Fatal(http.ListenAndServe(":"+port, nil))
}

func IndexHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/index.html")
}
