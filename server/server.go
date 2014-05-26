package server

import (
	"github.com/bsm/redeo"
	"github.com/tsileo/datadatabase/db"
	"github.com/tsileo/datadatabase/backend"
	"log"
	"fmt"
	"sync"
	"crypto/sha1"
	"crypto/rand"
	"net"
	"errors"
	"strconv"
	"path/filepath"
	"os"
	"os/signal"
	"strings"
)

var (
	ErrInvalidDB = errors.New("redeo: invalid DB index")
	ErrSomethingWentWrong = errors.New("redeo: something went wrong")
)

type ServerCtx struct {
	DB string
	TxID string
	Dbm *DBsManager
}

type DBsManager struct {
	dbpath string
	DBs map[string]*db.DB
	TxManagers map[string]*TxManager
	metaBackend backend.BlobHandler
	mem bool
	*sync.Mutex
}

func (dbm *DBsManager) GetDB(dbname string) *db.DB { 
	dbm.Lock()
	defer dbm.Unlock()
	cdb, exists := dbm.DBs[dbname]
	if !exists {
		var newdb *db.DB
		var err error
		if dbm.mem {
			newdb, err = db.NewMem()
		} else {
			newdb, err = db.New(filepath.Join(dbm.dbpath, dbname))
		}
		if err != nil {
			panic(err)
		}
		dbm.DBs[dbname] = newdb
		dbm.TxManagers[dbname] = NewTxManager(newdb, dbm.metaBackend)
		return newdb
	}
	return cdb
}

func (ctx *ServerCtx) GetDB() *db.DB {
	return ctx.Dbm.GetDB(ctx.DB)
}

func (ctx *ServerCtx) GetReqBuffer(name string) *ReqBuffer {
	// TODO(tsileo) remove this call
	ctx.GetDB()
	txmanager := ctx.Dbm.TxManagers[ctx.DB]
	return txmanager.GetReqBuffer(name)
}

func SetUpCtx(req *redeo.Request) {
	client := req.Client().RemoteAddr
	reqName := strings.ToLower(req.Name)
	if req.Client().Ctx == nil {
		log.Printf("server: new connection from: %+v", client)
		req.Client().Ctx = &ServerCtx{"default", "", dbmanager}
	}
	if !strings.HasPrefix(reqName, "b") {
		log.Printf("server: %+v command  with args %+v from client: %v\n", req.Name, req.Args, client)
	} else {
		log.Printf("server: %+v command from client: %v\n", req.Name, client)
	}
	bufferedCmd := map[string]bool{"sadd": true, "hmset": true, "hset": true, "ladd": true, "set": true}
	// TODO(tsileo) disable writing for these command, just keep them in the buffer
	_, buffered := bufferedCmd[reqName]
	if buffered {
		reqKey := req.Args[0]
		reqArgs := make([]string, len(req.Args)-1)
		copy(reqArgs, req.Args[1:])
		txID := req.Client().Ctx.(*ServerCtx).TxID
		rb := req.Client().Ctx.(*ServerCtx).GetReqBuffer(txID)
		rb.Add(reqName, reqKey, reqArgs)
	}
}

func CheckArgs(req *redeo.Request, argsCnt int) error {
	if len(req.Args) != argsCnt {
		return redeo.ErrWrongNumberOfArgs
	}
	return nil
}

func CheckMinArgs(req *redeo.Request, argsCnt int) error {
	if len(req.Args) < argsCnt {
		return redeo.ErrWrongNumberOfArgs
	}
	return nil
}

func SHA1(data []byte) string {
	h := sha1.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// NewID generate a random hash that can be used as random key
func NewID() string {
	data := make([]byte, 16)
	rand.Read(data)
	return SHA1(data)
}

func NewDBsManager(dbpath string, metaBackend backend.BlobHandler, testMode bool) *DBsManager {
	os.Mkdir(dbpath, 0700)
	return &DBsManager{dbpath, make(map[string]*db.DB), make(map[string]*TxManager), metaBackend, testMode, &sync.Mutex{}}	
}
var loadMetaBlobs sync.Once
var dbmanager *DBsManager

func New(addr, dbpath string, blobBackend backend.BlobHandler, metaBackend backend.BlobHandler, testMode bool, stop chan bool) {
	log.Println("server: starting...")
	dbmanager = NewDBsManager(dbpath, metaBackend, testMode)
	srv := redeo.NewServer(&redeo.Config{Addr: addr})
	srv.HandleFunc("ping", func(out *redeo.Responder, _ *redeo.Request) error {
		out.WriteInlineString("PONG")
		return nil
	})
	srv.HandleFunc("select", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 1)
		if err != nil {
			return err
		}
		req.Client().Ctx.(*ServerCtx).DB = req.Args[0]
		out.WriteOK()
		return nil
	})
	srv.HandleFunc("get", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 1)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		res, err := cdb.Get(req.Args[0])
		if err != nil {
			return ErrSomethingWentWrong
		}
		if res != nil {
			out.WriteString(string(res))	
		} else {
			out.WriteNil()
		}
		return nil
	})
	// Not needed as for now
	//srv.HandleFunc("getset", func(out *redeo.Responder, req *redeo.Request) error {
	//	SetUpCtx(req)
	//	err := CheckArgs(req, 2)
	//	if err != nil {
	//		return err
	//	}
	//	cdb := req.Client().Ctx.(*ServerCtx).GetDB()
	//	res, err := cdb.Getset(req.Args[0], req.Args[1])
	//	if err != nil {
	//		return ErrSomethingWentWrong
	//	}
	//	if res != nil {
	//		out.WriteString(string(res))	
	//	} else {
	//		out.WriteNil()
	//	}
	//	return nil
	//})
	srv.HandleFunc("set", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 2)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		err  = cdb.Put(req.Args[0], req.Args[1])
		if err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteOK()
		return nil
	})
	srv.HandleFunc("range", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 3)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		limit, err := strconv.Atoi(req.Args[2])
		if err != nil {
			return ErrSomethingWentWrong
		}
		kvs, err := cdb.GetStringRange(req.Args[0], req.Args[1], limit)
		if err != nil {
			return ErrSomethingWentWrong
		}
		if len(kvs) == 0 {
			out.WriteNil()
			return nil
		}
		out.WriteBulkLen(len(kvs))
		for _, skv := range kvs {
			out.WriteString(skv.Value)
		}
		return nil
	})
	srv.HandleFunc("sadd", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckMinArgs(req, 2)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		cmdArgs := make([]string, len(req.Args)-1)
		copy(cmdArgs, req.Args[1:])
		cnt := cdb.Sadd(req.Args[0], cmdArgs...)
		if err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteInt(cnt)
		return nil
	})
	srv.HandleFunc("scard", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 1)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		cnt, err := cdb.Scard(req.Args[0])
		if err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteInt(cnt)
		return nil
	})
	srv.HandleFunc("smembers", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 1)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		members := cdb.Smembers(req.Args[0])
		//if err != nil {
		//	return ErrSomethingWentWrong
		//}
		if len(members) != 0 {
			out.WriteBulkLen(len(members))
			for _, member := range members {
				out.WriteString(string(member))
			}
		} else {
			out.WriteNil()
		}
		return nil
	})
	srv.HandleFunc("hset", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 3)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		cnt, err  := cdb.Hset(req.Args[0], req.Args[1], req.Args[2])
		if err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteInt(cnt)
		return nil
	})
	srv.HandleFunc("hmset", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckMinArgs(req, 3)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		cmdArgs := make([]string, len(req.Args)-1)
		copy(cmdArgs, req.Args[1:])
		cnt, err  := cdb.Hmset(req.Args[0], cmdArgs...)
		if err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteInt(cnt)
		return nil
	})
	srv.HandleFunc("hlen", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckMinArgs(req, 1)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		cnt, err  := cdb.Hlen(req.Args[0])
		if err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteInt(cnt)
		return nil
	})
	srv.HandleFunc("hget", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 2)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		res, err := cdb.Hget(req.Args[0], req.Args[1])
		if err != nil {
			return ErrSomethingWentWrong
		}
		if res != nil {
			out.WriteString(string(res))
		} else {
			out.WriteNil()
		}
		return nil
	})
	srv.HandleFunc("hgetall", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 1)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		kvs, err := cdb.Hgetall(req.Args[0])
		if err != nil {
			return ErrSomethingWentWrong
		}
		if len(kvs) != 0 {
			out.WriteBulkLen(len(kvs) * 2)
			for _, kv := range kvs {
				out.WriteString(kv.Key)
				out.WriteString(kv.Value)
			}
		} else {
			out.WriteNil()
		}
		return nil
	})
	srv.HandleFunc("hscan", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 3)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		limit, err := strconv.Atoi(req.Args[2])
		if err != nil {
			return ErrSomethingWentWrong
		}
		hkeys, err := cdb.Hscan(req.Args[0], req.Args[1], limit)
		if err != nil {
			return ErrSomethingWentWrong
		}
		if len(hkeys) != 0 {
			out.WriteBulkLen(len(hkeys))
			for _, hkey := range hkeys {
				out.WriteString(string(hkey))
			}
		} else {
			out.WriteNil()
		}
		return nil
	})
	srv.HandleFunc("bsize", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 0)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		size, err := cdb.GetBlobsSize()
		if err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteInt(int(size))
		return nil
	})
	srv.HandleFunc("bcnt", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 0)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		cnt, err := cdb.GetBlobsCnt()
		if err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteInt(int(cnt))
		return nil
	})
	srv.HandleFunc("bput", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 1)
		if err != nil {
			return err
		}
		blob := []byte(req.Args[0])
		log.Printf("server: BPUT blob len: %v", len(blob))
		sha := SHA1(blob)
		err  = blobBackend.Put(sha, blob)
		if err != nil {
			log.Printf("server: Error BPUT:%v", err)
			return ErrSomethingWentWrong
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		cdb.IncrBlobsCnt(1)
		cdb.IncrBlobsSize(len(blob))
		out.WriteString(sha)
		return nil
	})
	srv.HandleFunc("bget", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 1)
		if err != nil {
			return err
		}
		blob, err  := blobBackend.Get(req.Args[0])
		if err != nil {
			log.Printf("Error bget %v: %v", req.Args[0], err)
			return ErrSomethingWentWrong
		}
		out.WriteString(string(blob))
		return nil
	})
	srv.HandleFunc("bexists", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 1)
		if err != nil {
			return err
		}
		exists := blobBackend.Exists(req.Args[0])
		res := 0
		if exists {
			res = 1
		}
		out.WriteInt(res)
		return nil
	})
	
	srv.HandleFunc("llen", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 1)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		card, err  := cdb.Llen(req.Args[0])
		if err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteInt(card)
		return nil
	})
	srv.HandleFunc("ladd", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 3)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		cindex, err := strconv.Atoi(req.Args[1])
		if err != nil {
			return ErrSomethingWentWrong
		}
		err  = cdb.Ladd(req.Args[0], cindex, req.Args[2])
		if err != nil {
			return ErrSomethingWentWrong
		}
		log.Printf("ladd done")
		out.WriteOK()
		return nil
	})
	srv.HandleFunc("lindex", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 2)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		cindex, err := strconv.Atoi(req.Args[1])
		if err != nil {
			return ErrSomethingWentWrong
		}
		res, err := cdb.Lindex(req.Args[0], cindex)
		if err != nil {
			return ErrSomethingWentWrong
		}
		if res != nil {
			out.WriteString(string(res))	
		} else {
			out.WriteNil()
		}
		return nil
	})
	srv.HandleFunc("lrange", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 4)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		limit, err := strconv.Atoi(req.Args[3])
		if err != nil {
			return ErrSomethingWentWrong
		}
		kvs, err := cdb.GetListRange(req.Args[0], req.Args[1], req.Args[2], limit)
		if err != nil {
			return ErrSomethingWentWrong
		}
		if len(kvs) == 0 {
			out.WriteNil()	
		} else {
			out.WriteBulkLen(len(kvs))
			for _, kv := range kvs {
				out.WriteString(kv.Value)
			}
		}
		return nil
	})
	srv.HandleFunc("llast", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 4)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		limit, err := strconv.Atoi(req.Args[3])
		if err != nil {
			return ErrSomethingWentWrong
		}
		kv, err := cdb.GetListRangeLast(req.Args[0], req.Args[1], req.Args[2], limit)
		if err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteString(kv.Value)
		return nil
	})
	srv.HandleFunc("lprev", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 2)
		if err != nil {
			return err
		}
		start, err := strconv.Atoi(req.Args[1])
		if err != nil {
			return ErrSomethingWentWrong
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		res := cdb.Lprev(req.Args[0], start)
		out.WriteString(res)
		return nil
	})
	srv.HandleFunc("liter", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckMinArgs(req, 1)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		if len(req.Args) == 1 {
			vals, err := cdb.Liter(req.Args[0])
			if err != nil {
				return ErrSomethingWentWrong
			}
			if len(vals) == 0 {
				out.WriteNil()	
			} else {
				out.WriteBulkLen(len(vals))
				for _, bval := range vals {
					out.WriteString(string(bval))
				}
			}
			return nil
		}
		if len(req.Args) == 3 {
			// WITH INDEX
			if strings.ToLower(req.Args[1]) != "with" {
				return ErrSomethingWentWrong
			}
			ivs, err := cdb.LiterWithIndex(req.Args[0])
			if err != nil {
				return ErrSomethingWentWrong
			}
			if len(ivs) == 0 {
				out.WriteNil()	
			} else {
				out.WriteBulkLen(len(ivs) * 2)
				for _, iv := range ivs {
					out.WriteString(strconv.Itoa(iv.Index))
					out.WriteString(iv.Value)
				}
			}
			return nil
		}
		return ErrSomethingWentWrong
	})
	srv.HandleFunc("lmrange", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 4)
		if err != nil {
			return err
		}
		cdb := req.Client().Ctx.(*ServerCtx).GetDB()
		start, err := strconv.Atoi(req.Args[1])
		if err != nil {
			return ErrSomethingWentWrong
		}
		end, err := strconv.Atoi(req.Args[2])
		if err != nil {
			return ErrSomethingWentWrong
		}
		limit, err := strconv.Atoi(req.Args[3])
		if err != nil {
			return ErrSomethingWentWrong
		}
		ivs, err := cdb.GetListMinRange(req.Args[0], start, end, limit)
		if err != nil {
			return ErrSomethingWentWrong
		}
		if len(ivs) == 0 {
			out.WriteNil()	
		} else {
			out.WriteBulkLen(len(ivs) * 2)
			for _, iv := range ivs {
				out.WriteString(strconv.Itoa(iv.Index))
				out.WriteString(iv.Value)
			}
		}
		return nil
	})
	srv.HandleFunc("size", func(out *redeo.Responder, _ *redeo.Request) error {
		out.WriteInt(0)
		return nil
	})
	srv.HandleFunc("shutdown", func(out *redeo.Responder, _ *redeo.Request) error {
		stop <-true
		out.WriteOK()
		return nil
	})

	srv.HandleFunc("init", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 0)
		if err != nil {
			return err
		}
		txID := req.Client().Ctx.(*ServerCtx).TxID
		if err := req.Client().Ctx.(*ServerCtx).GetReqBuffer(txID).Load(); err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteOK()
		return nil
	})

	srv.HandleFunc("txinit", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckMinArgs(req, 0)
		if err != nil {
			return err
		}
		switch {
		case len(req.Args) == 0:
			newID := NewID()
			req.Client().Ctx.(*ServerCtx).TxID = newID
			out.WriteString(newID)

		case len(req.Args) == 1:
			req.Client().Ctx.(*ServerCtx).TxID = req.Args[0]
			out.WriteOK()

		case len(req.Args) > 1:
			return ErrSomethingWentWrong
		
		}
		return nil
	})
	srv.HandleFunc("txdiscard", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 0)
		if err != nil {
			return err
		}
		txID := req.Client().Ctx.(*ServerCtx).TxID
		req.Client().Ctx.(*ServerCtx).GetReqBuffer(txID).Reset()
		out.WriteOK()
		return nil
	})
	srv.HandleFunc("txcommit", func(out *redeo.Responder, req *redeo.Request) error {
		SetUpCtx(req)
		err := CheckArgs(req, 0)
		if err != nil {
			return err
		}
		txID := req.Client().Ctx.(*ServerCtx).TxID
		rb := req.Client().Ctx.(*ServerCtx).GetReqBuffer(txID)
		err = rb.Save()
		if err != nil {
			return ErrSomethingWentWrong
		}
		out.WriteOK()
		return nil
	})

	log.Printf("server: listening on tcp://%s", srv.Addr())
	//log.Fatal(srv.ListenAndServe())

	listener, err := net.Listen("tcp", srv.Addr())
	if err != nil {
		panic(err)
	}
	cs := make(chan os.Signal, 1)
	signal.Notify(cs, os.Interrupt)
	if stop != nil {
		go func() {
			for {
				select {
				case _ = <-stop:
					break
				case sig := <- cs:
					log.Printf("server: Captured %v\n", sig)
					break
				}
				log.Println("server: closing DBs first...")
				for _, cdb := range dbmanager.DBs {
					cdb.Close()
				}
				log.Println("server: closing backends...")
				blobBackend.Close()
				metaBackend.Close()
				log.Println("server: shutting down...")
				err := listener.Close()
				if err != nil {
					log.Println(err.Error())
				}
				os.Exit(0)
			}
		}()
	}
	errs := make(chan error)
	srv.Serve(errs, listener)
}
