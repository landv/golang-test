package main

import (
	"crypto/tls"
	"encoding/binary"
	"flag"
	"fmt"
	"github.com/chai2010/winsvc"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"time"
)

/**
先把lanproxy-go-client的东东加进来
*/
const (
	/* 心跳消息 */
	TYPE_HEARTBEAT = 0x07

	/* 认证消息，检测clientKey是否正确 */
	C_TYPE_AUTH = 0x01

	/* 代理后端服务器建立连接消息 */
	TYPE_CONNECT = 0x03

	/* 代理后端服务器断开连接消息 */
	TYPE_DISCONNECT = 0x04

	/* 代理数据传输 */
	P_TYPE_TRANSFER = 0x05

	/* 用户与代理服务器以及代理客户端与真实服务器连接是否可写状态同步 */
	C_TYPE_WRITE_CONTROL = 0x06

	//协议各字段长度
	LEN_SIZE = 4

	TYPE_SIZE = 1

	SERIAL_NUMBER_SIZE = 8

	URI_LENGTH_SIZE = 1

	//心跳周期，服务器端空闲连接如果60秒没有数据上报就会关闭连接
	HEARTBEAT_INTERVAL = 30
)

type LPMessageHandler struct {
	connPool    *ConnHandlerPool
	connHandler *ConnHandler
	clientKey   string
	die         chan struct{}
}

type Message struct {
	Type         byte
	SerialNumber uint64
	Uri          string
	Data         []byte
}

type ProxyConnPooler struct {
	addr string
	conf *tls.Config
}

func start(key string, ip string, port int, conf *tls.Config) {
	connPool := &ConnHandlerPool{Size: 100, Pooler: &ProxyConnPooler{addr: ip + ":" + strconv.Itoa(port), conf: conf}}
	connPool.Init()
	connHandler := &ConnHandler{}
	for {
		//cmd connection
		conn := connect(key, ip, port, conf)
		connHandler.conn = conn
		messageHandler := LPMessageHandler{connPool: connPool}
		messageHandler.connHandler = connHandler
		messageHandler.clientKey = key
		messageHandler.startHeartbeat()
		log.Println("start listen cmd message:", messageHandler)
		connHandler.Listen(conn, &messageHandler)
	}
}

func connect(key string, ip string, port int, conf *tls.Config) net.Conn {
	for {
		var conn net.Conn
		var err error
		p := strconv.Itoa(port)
		if conf != nil {
			conn, err = tls.Dial("tcp", ip+":"+p, conf)
		} else {
			conn, err = net.Dial("tcp", ip+":"+p)
		}
		if err != nil {
			log.Println("Error dialing", err.Error())
			time.Sleep(time.Second * 3)
			continue
		}

		return conn
	}
}

func (messageHandler *LPMessageHandler) Encode(msg interface{}) []byte {
	if msg == nil {
		return []byte{}
	}

	message := msg.(Message)
	uriBytes := []byte(message.Uri)
	bodyLen := TYPE_SIZE + SERIAL_NUMBER_SIZE + URI_LENGTH_SIZE + len(uriBytes) + len(message.Data)
	data := make([]byte, LEN_SIZE, bodyLen+LEN_SIZE)
	binary.BigEndian.PutUint32(data, uint32(bodyLen))
	data = append(data, message.Type)
	snBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(snBytes, message.SerialNumber)
	data = append(data, snBytes...)
	data = append(data, byte(len(uriBytes)))
	data = append(data, uriBytes...)
	data = append(data, message.Data...)
	return data
}

func (messageHandler *LPMessageHandler) Decode(buf []byte) (interface{}, int) {
	lenBytes := buf[0:LEN_SIZE]
	bodyLen := binary.BigEndian.Uint32(lenBytes)
	if uint32(len(buf)) < bodyLen+LEN_SIZE {
		return nil, 0
	}
	n := int(bodyLen + LEN_SIZE)
	body := buf[LEN_SIZE:n]
	msg := Message{}
	msg.Type = body[0]
	msg.SerialNumber = binary.BigEndian.Uint64(body[TYPE_SIZE : SERIAL_NUMBER_SIZE+TYPE_SIZE])
	uriLen := uint8(body[SERIAL_NUMBER_SIZE+TYPE_SIZE])
	msg.Uri = string(body[SERIAL_NUMBER_SIZE+TYPE_SIZE+URI_LENGTH_SIZE : SERIAL_NUMBER_SIZE+TYPE_SIZE+URI_LENGTH_SIZE+uriLen])
	msg.Data = body[SERIAL_NUMBER_SIZE+TYPE_SIZE+URI_LENGTH_SIZE+uriLen:]
	return msg, n
}

func (messageHandler *LPMessageHandler) MessageReceived(connHandler *ConnHandler, msg interface{}) {
	message := msg.(Message)
	switch message.Type {
	case TYPE_CONNECT:
		go func() {
			log.Println("received connect message:", message.Uri, "=>", string(message.Data))
			addr := string(message.Data)
			realServerMessageHandler := &RealServerMessageHandler{LpConnHandler: connHandler, ConnPool: messageHandler.connPool, UserId: message.Uri, ClientKey: messageHandler.clientKey}
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				log.Println("connect realserver failed", err)
				realServerMessageHandler.ConnFailed()
			} else {
				connHandler := &ConnHandler{}
				connHandler.conn = conn
				connHandler.Listen(conn, realServerMessageHandler)
			}
		}()
	case P_TYPE_TRANSFER:
		if connHandler.NextConn != nil {
			connHandler.NextConn.Write(message.Data)
		}
	case TYPE_DISCONNECT:
		if connHandler.NextConn != nil {
			connHandler.NextConn.NextConn = nil
			connHandler.NextConn.conn.Close()
			connHandler.NextConn = nil
		}
		if messageHandler.clientKey == "" {
			messageHandler.connPool.Return(connHandler)
		}
	}
}

func (messageHandler *LPMessageHandler) ConnSuccess(connHandler *ConnHandler) {
	log.Println("connSuccess, clientkey:", messageHandler.clientKey)
	if messageHandler.clientKey != "" {
		msg := Message{Type: C_TYPE_AUTH}
		msg.Uri = messageHandler.clientKey
		connHandler.Write(msg)
	}
}

func (messageHandler *LPMessageHandler) ConnError(connHandler *ConnHandler) {
	log.Println("connError:", connHandler)
	if messageHandler.die != nil {
		close(messageHandler.die)
	}

	if connHandler.NextConn != nil {
		connHandler.NextConn.NextConn = nil
		connHandler.NextConn.conn.Close()
		connHandler.NextConn = nil
	}

	connHandler.messageHandler = nil
	messageHandler.connHandler = nil
	time.Sleep(time.Second * 3)
}

func (messageHandler *LPMessageHandler) startHeartbeat() {
	log.Println("start heartbeat:", messageHandler.connHandler)
	messageHandler.die = make(chan struct{})
	go func() {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("run time panic: %v", err)
				debug.PrintStack()
			}
		}()
		for {
			select {
			case <-time.After(time.Second * HEARTBEAT_INTERVAL):
				if time.Now().Unix()-messageHandler.connHandler.ReadTime >= 2*HEARTBEAT_INTERVAL {
					log.Println("proxy connection timeout:", messageHandler.connHandler, time.Now().Unix()-messageHandler.connHandler.ReadTime)
					messageHandler.connHandler.conn.Close()
					return
				}
				msg := Message{Type: TYPE_HEARTBEAT}
				messageHandler.connHandler.Write(msg)
			case <-messageHandler.die:
				return
			}
		}
	}()
}

func (pooler *ProxyConnPooler) Create(pool *ConnHandlerPool) (*ConnHandler, error) {
	var conn net.Conn
	var err error
	if pooler.conf != nil {
		conn, err = tls.Dial("tcp", pooler.addr, pooler.conf)
	} else {
		conn, err = net.Dial("tcp", pooler.addr)
	}

	if err != nil {
		log.Println("Error dialing", err.Error())
		return nil, err
	} else {
		messageHandler := LPMessageHandler{connPool: pool}
		connHandler := &ConnHandler{}
		connHandler.Active = true
		connHandler.conn = conn
		connHandler.messageHandler = interface{}(&messageHandler).(MessageHandler)
		messageHandler.connHandler = connHandler
		messageHandler.startHeartbeat()
		go func() {
			connHandler.Listen(conn, &messageHandler)
		}()
		return connHandler, nil
	}
}

func (pooler *ProxyConnPooler) Remove(conn *ConnHandler) {
	conn.conn.Close()
}

func (pooler *ProxyConnPooler) IsActive(conn *ConnHandler) bool {
	return conn.Active
}

/**
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build fuckRegisServices.go

*/
var (
	server *http.Server
)
var (
	appPath              string
	flagServiceName      = flag.String("service-name", "myserver", "Set service name")
	flagServiceDesc      = flag.String("service-desc", "myserver service", "Set service description")
	flagServiceInstall   = flag.Bool("service-install", false, "Install service")
	flagServiceUninstall = flag.Bool("service-remove", false, "Remove service")
	flagServiceStart     = flag.Bool("service-start", false, "Start service")
	flagServiceStop      = flag.Bool("service-stop", false, "Stop service")
)

func init() {
	// change to current dir
	var err error
	if appPath, err = winsvc.GetAppPath(); err != nil {
		log.Fatal(err)
	}
	if err := os.Chdir(filepath.Dir(appPath)); err != nil {
		log.Fatal(err)
	}
}
func main() {
	flag.Parse()
	// install service
	if *flagServiceInstall {
		if err := winsvc.InstallService(appPath, *flagServiceName, *flagServiceDesc); err != nil {
			log.Fatalf("installService(%s, %s): %v\n", *flagServiceName, *flagServiceDesc, err)
		}
		fmt.Printf("Done\n")
		return
	}
	// remove service
	if *flagServiceUninstall {
		if err := winsvc.RemoveService(*flagServiceName); err != nil {
			log.Fatalln("removeService:", err)
		}
		fmt.Printf("Done\n")
		return
	}
	// start service
	if *flagServiceStart {
		if err := winsvc.StartService(*flagServiceName); err != nil {
			log.Fatalln("startService:", err)
		}
		fmt.Printf("Done\n")
		return
	}
	// stop service
	if *flagServiceStop {
		if err := winsvc.StopService(*flagServiceName); err != nil {
			log.Fatalln("stopService:", err)
		}
		fmt.Printf("Done\n")
		return
	}
	// run as service
	if !winsvc.InServiceMode() {
		log.Println("main:", "runService")
		if err := winsvc.RunAsService(*flagServiceName, StartServer, StopServer, false); err != nil {
			log.Fatalf("svc.Run: %v\n", err)
		}
		return
	}
	// run as normal
	StartServer()
}
func StartServer() {
	//start(c.String("k"), c.String("s"), c.Int("p"), conf)
	var conf *tls.Config
	start("key", "IP", 4900, conf)
	//log.Println("StartServer, port = 8080")
	//http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	//	fmt.Fprintln(w, "winsrv server", time.Now())
	//})
	//server = &http.Server{Addr: ":8080"}
	//server.ListenAndServe()

	//log.Println("lanproxy - help you expose a local server behind a NAT or firewall to the internet")
	//app := cli.NewApp()
	//app.Name = "lanproxy"
	//app.Flags = []cli.Flag{
	//	cli.StringFlag{
	//		Name:  "k",
	//		Value: "",
	//		Usage: "client key",
	//	},
	//	cli.StringFlag{
	//		Name:  "s",
	//		Value: "",
	//		Usage: "proxy server host",
	//	},
	//	cli.IntFlag{
	//		Name:  "p",
	//		Value: 4900,
	//		Usage: "proxy server port",
	//	}, cli.StringFlag{
	//		Name:  "ssl",
	//		Value: "false",
	//		Usage: "enable ssl",
	//	}, cli.StringFlag{
	//		Name:  "cer",
	//		Value: "",
	//		Usage: "ssl cert path, default skip verify certificate",
	//	}}
	//app.Usage = "help you expose a local server behind a NAT or firewall to the internet"
	//app.Action = func(c *cli.Context) error {
	//	if c.String("s") == "" {
	//		log.Println("server ip addr is required, use -s")
	//		log.Println("exit")
	//		return nil
	//	}
	//	if c.String("k") == "" {
	//		log.Println("clientkey is required, use -k")
	//		log.Println("exit")
	//		return nil
	//	}
	//	log.Println("client key:", c.String("k"))
	//	log.Println("server addr:", c.String("s"))
	//	log.Println("server port:", c.Int("p"))
	//	log.Println("enable ssl:", c.String("ssl"))
	//	cerPath := c.String("cer")
	//	if c.String("cer") == "" {
	//		cerPath = "certificate path is null, skip verify certificate"
	//	}
	//	log.Println("ssl cer path:", cerPath)
	//	var conf *tls.Config
	//	if c.String("ssl") == "true" {
	//		skipVerify := false
	//		if c.String("cer") == "" {
	//			skipVerify = true
	//		}
	//		conf = &tls.Config{
	//			InsecureSkipVerify: skipVerify,
	//		}
	//
	//		if c.String("cer") != "" {
	//			cert, err := ioutil.ReadFile(c.String("cer"))
	//			if err != nil {
	//				log.Fatalf("Couldn't load file", err)
	//				return nil
	//			}
	//			certPool := x509.NewCertPool()
	//			certPool.AppendCertsFromPEM(cert)
	//			conf.ClientCAs = certPool
	//		}
	//	}
	//	start(c.String("k"), c.String("s"), c.Int("p"), conf)
	//	return nil
	//}
	//
	//app.Run(os.Args)
}
func StopServer() {
	//if server != nil {
	//	server.Shutdown(context.Background()) // Go 1.8+
	//}
	//log.Println("StopServer")

}
