// mongoplus-loadtest 在随机命名的专用数据库执行有限时长的混合读写压测。
// 它不会连接业务数据库；输出吞吐、错误率、分操作延迟分位及 Go 堆内存峰值。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type config struct {
	URI                                          string
	Seed, Concurrency, ReadPercent, PayloadBytes int
	Duration, Warmup, Timeout                    time.Duration
	Output                                       string
	MaxP95, MaxP99                               float64
	MaxErrorRate                                 float64
}

type row struct {
	ID      int64  `bson:"_id"`
	Payload string `bson:"payload"`
	Version int64  `bson:"version"`
}

// 2048 个对数桶，分位数返回桶上界，误差小于约 2.2%；内存不随运行时长增长。
type histogram struct {
	Buckets       [2048]uint64
	Count, Errors uint64
	TotalSeconds  float64
	Max           time.Duration
}

func (h *histogram) add(elapsed time.Duration, err error) {
	index := min(len(h.Buckets)-1, int(math.Log2(float64(max(elapsed.Nanoseconds(), 1)))*32))
	h.Buckets[index]++
	h.Count++
	h.TotalSeconds += elapsed.Seconds()
	h.Max = max(h.Max, elapsed)
	if err != nil {
		h.Errors++
	}
}
func (h *histogram) merge(other histogram) {
	for i, n := range other.Buckets {
		h.Buckets[i] += n
	}
	h.Count += other.Count
	h.Errors += other.Errors
	h.TotalSeconds += other.TotalSeconds
	h.Max = max(h.Max, other.Max)
}
func (h histogram) quantile(p float64) float64 {
	target := uint64(math.Ceil(float64(h.Count) * p))
	if target == 0 {
		return 0
	}
	var count uint64
	for i, n := range h.Buckets {
		count += n
		if count >= target {
			return min(float64(h.Max)/float64(time.Millisecond), math.Pow(2, float64(i+1)/32)/1e6)
		}
	}
	return 0
}

type latency struct {
	Count  uint64  `json:"count"`
	Errors uint64  `json:"errors"`
	P50    float64 `json:"p50Ms"`
	P95    float64 `json:"p95Ms"`
	P99    float64 `json:"p99Ms"`
	Max    float64 `json:"maxMs"`
	Mean   float64 `json:"meanMs"`
}

func (h histogram) summary() latency {
	mean := 0.0
	if h.Count > 0 {
		mean = h.TotalSeconds * 1000 / float64(h.Count)
	}
	return latency{h.Count, h.Errors, h.quantile(.5), h.quantile(.95), h.quantile(.99), float64(h.Max) / float64(time.Millisecond), mean}
}

type report struct {
	StartedAt       time.Time `json:"startedAt"`
	LibraryVersion  string    `json:"libraryVersion"`
	GoVersion       string    `json:"goVersion"`
	Platform        string    `json:"platform"`
	CPUs            int       `json:"cpus"`
	Database        string    `json:"database"`
	Seed            int       `json:"seed"`
	Concurrency     int       `json:"concurrency"`
	ReadPercent     int       `json:"readPercent"`
	PayloadBytes    int       `json:"payloadBytes"`
	WarmupSeconds   float64   `json:"warmupSeconds"`
	DurationSeconds float64   `json:"durationSeconds"`
	QPS             float64   `json:"qps"`
	ErrorRate       float64   `json:"errorRate"`
	All             latency   `json:"all"`
	Reads           latency   `json:"reads"`
	Writes          latency   `json:"writes"`
	PeakHeapAlloc   uint64    `json:"peakHeapAllocBytes"`
	EndHeapAlloc    uint64    `json:"endHeapAllocBytes"`
	EndRuntimeSys   uint64    `json:"endRuntimeSysBytes"`
	PeakGoroutines  int       `json:"peakGoroutines"`
	GCs             uint32    `json:"gcCount"`
	Passed          bool      `json:"passed"`
	Failures        []string  `json:"failures"`
	Notes           []string  `json:"notes"`
}

func parse(args []string) (config, error) {
	c := config{}
	flags := flag.NewFlagSet("mongoplus-loadtest", flag.ContinueOnError)
	flags.StringVar(&c.URI, "uri", "", "专用 MongoDB 实例 URI（不写入报告）")
	flags.IntVar(&c.Seed, "seed", 10000, "预置文档数，1 到 100000000")
	flags.IntVar(&c.Concurrency, "concurrency", 16, "并发数，1 到 4096")
	flags.IntVar(&c.ReadPercent, "read-percent", 80, "按主键读取比例，0 到 100，其余为原子递增更新")
	flags.IntVar(&c.PayloadBytes, "payload-bytes", 256, "每个文档的载荷字节数，1 到 1048576")
	flags.DurationVar(&c.Duration, "duration", 30*time.Second, "测量时长，100ms 到 24h")
	flags.DurationVar(&c.Warmup, "warmup", 5*time.Second, "预热时长，0 到 1h，不计入报告")
	flags.DurationVar(&c.Timeout, "timeout", 5*time.Second, "每次操作的超时")
	flags.StringVar(&c.Output, "output", "", "可选的 JSON 报告文件")
	flags.Float64Var(&c.MaxP95, "max-p95-ms", 0, "整体 P95 上限；0 不设门槛")
	flags.Float64Var(&c.MaxP99, "max-p99-ms", 0, "整体 P99 上限；0 不设门槛")
	flags.Float64Var(&c.MaxErrorRate, "max-error-rate", 0, "允许的错误率，0 到 1，默认不允许错误")
	if err := flags.Parse(args); err != nil {
		return c, err
	}
	if flags.NArg() != 0 || c.URI == "" || c.Seed < 1 || c.Seed > 100000000 || c.Concurrency < 1 || c.Concurrency > 4096 || c.ReadPercent < 0 || c.ReadPercent > 100 || c.PayloadBytes < 1 || c.PayloadBytes > 1<<20 || c.Duration < 100*time.Millisecond || c.Duration > 24*time.Hour || c.Warmup < 0 || c.Warmup > time.Hour || c.Timeout <= 0 || c.MaxP95 < 0 || c.MaxP99 < 0 || c.MaxErrorRate < 0 || c.MaxErrorRate > 1 {
		return c, fmt.Errorf("参数不合法；必须显式提供测试专用 -uri，并使用有效的数据量、并发、时长及阈值")
	}
	for _, v := range []float64{c.MaxP95, c.MaxP99, c.MaxErrorRate} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return c, fmt.Errorf("阈值必须为有限数值")
		}
	}
	return c, nil
}

func measure(c config, repo *mp.Repository[row, int64], duration time.Duration) (histogram, histogram) {
	deadline := time.Now().Add(duration)
	var wg sync.WaitGroup
	readStats, writeStats := make([]histogram, c.Concurrency), make([]histogram, c.Concurrency)
	for worker := range c.Concurrency {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			random := rand.New(rand.NewPCG(uint64(worker+1), uint64(time.Now().UnixNano())))
			for time.Now().Before(deadline) {
				id := random.Int64N(int64(c.Seed)) + 1
				ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
				start := time.Now()
				var err error
				if random.IntN(100) < c.ReadPercent {
					_, err = repo.FindById(ctx, id)
					readStats[worker].add(time.Since(start), err)
				} else {
					_, err = repo.UpdateById(ctx, id, field.NewNumeric[int64]("version").Inc(1))
					writeStats[worker].add(time.Since(start), err)
				}
				cancel()
			}
		}(worker)
	}
	wg.Wait()
	var reads, writes histogram
	for i := range c.Concurrency {
		reads.merge(readStats[i])
		writes.merge(writeStats[i])
	}
	return reads, writes
}

func run(c config) (result report, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := "mongo_plus_loadtest_" + bson.NewObjectID().Hex()
	db, err := mp.Connect(ctx, mp.Config{URI: c.URI, Database: name, MaxPoolSize: uint64(c.Concurrency + 10)})
	if err != nil {
		return result, err
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer closeCancel()
		err = errors.Join(err, db.Native().Drop(closeCtx), db.Close(closeCtx))
	}()
	repo := mp.MustRepository[row, int64](db, mp.CollectionConfig{Name: "documents", ID: mp.IDConfig{Type: mp.IDTypeInt64, Generation: mp.IDGenerationManual}})
	batchSize := min(1000, max(1, (8<<20)/c.PayloadBytes))
	for first := 1; first <= c.Seed; first += batchSize {
		rows := make([]*row, 0, min(batchSize, c.Seed-first+1))
		payload := strings.Repeat("x", c.PayloadBytes)
		for id := first; id <= c.Seed && id < first+batchSize; id++ {
			rows = append(rows, &row{ID: int64(id), Payload: payload})
		}
		batchCtx, batchCancel := context.WithTimeout(context.Background(), c.Timeout)
		err := repo.CreateBatch(batchCtx, rows)
		batchCancel()
		if err != nil {
			return result, err
		}
	}
	if c.Warmup > 0 {
		measure(c, repo, c.Warmup)
	}
	var initial, final runtime.MemStats
	runtime.ReadMemStats(&initial)
	peakHeap, peakGoroutines := initial.HeapAlloc, runtime.NumGoroutine()
	stop, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				peakHeap = max(peakHeap, stats.HeapAlloc)
				peakGoroutines = max(peakGoroutines, runtime.NumGoroutine())
			}
		}
	}()
	started := time.Now()
	reads, writes := measure(c, repo, c.Duration)
	elapsed := time.Since(started)
	close(stop)
	<-sampled
	runtime.ReadMemStats(&final)
	all := reads
	all.merge(writes)
	errorRate := float64(all.Errors) / float64(max(all.Count, 1))
	failures := []string{}
	if c.MaxP95 > 0 && all.quantile(.95) > c.MaxP95 {
		failures = append(failures, "P95 exceeds threshold")
	}
	if c.MaxP99 > 0 && all.quantile(.99) > c.MaxP99 {
		failures = append(failures, "P99 exceeds threshold")
	}
	if errorRate > c.MaxErrorRate {
		failures = append(failures, "error rate exceeds threshold")
	}
	if all.Count == 0 {
		failures = append(failures, "no completed operations")
	}
	sort.Strings(failures)
	result = report{
		StartedAt:       started.UTC(),
		LibraryVersion:  mp.Version,
		GoVersion:       runtime.Version(),
		Platform:        runtime.GOOS + "/" + runtime.GOARCH,
		CPUs:            runtime.NumCPU(),
		Database:        name,
		Seed:            c.Seed,
		Concurrency:     c.Concurrency,
		ReadPercent:     c.ReadPercent,
		PayloadBytes:    c.PayloadBytes,
		WarmupSeconds:   c.Warmup.Seconds(),
		DurationSeconds: elapsed.Seconds(),
		QPS:             float64(all.Count) / elapsed.Seconds(),
		ErrorRate:       errorRate,
		All:             all.summary(),
		Reads:           reads.summary(),
		Writes:          writes.summary(),
		PeakHeapAlloc:   max(peakHeap, final.HeapAlloc),
		EndHeapAlloc:    final.HeapAlloc,
		EndRuntimeSys:   final.Sys,
		PeakGoroutines:  peakGoroutines,
		GCs:             final.NumGC - initial.NumGC,
		Passed:          len(failures) == 0,
		Failures:        failures,
		Notes: []string{
			"Synthetic primary-key read/update workload; cache disabled; not a business capacity guarantee.",
			"Latency percentiles are histogram upper bounds with less than approximately 2.2% bucket error.",
			"Heap metrics include the load generator; RuntimeSys is not process RSS. Database is dropped after the run.",
		},
	}
	return result, nil
}

func main() {
	c, err := parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	result, err := run(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
	if c.Output != "" {
		if err := os.WriteFile(c.Output, append(encoded, '\n'), 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if !result.Passed {
		os.Exit(1)
	}
}
