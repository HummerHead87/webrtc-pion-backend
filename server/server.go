package main

import (
	server "hummerhead87/chat"
	"log"
	"net/http"
	"os"

	"github.com/99designs/gqlgen/handler"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v2"
	"github.com/rs/cors"
)

const defaultPort = "4000"

var (
	// Media engine
	mEngine webrtc.MediaEngine
	// API object
	api *webrtc.API
)

func init() {
	// Generate pem file for https
	// genPem()

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
		port = defaultPort
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
	handler := cors.AllowAll().Handler(mux)

	log.Printf("connect to http://localhost:%s/playground for GraphQL playground", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))

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
