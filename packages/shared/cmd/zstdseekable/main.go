package main

import (
    "bytes"
    "context"
    "crypto/sha512"
    "errors"
    "flag"
    "io"
    "log"
    "os"
    "strconv"
    "strings"

    "github.com/SaveTheRbtz/fastcdc-go"
    "github.com/klauspost/compress/zstd"
    "github.com/schollz/progressbar/v3"
    "go.uber.org/zap"
    "golang.org/x/term"

    seekable "github.com/SaveTheRbtz/zstd-seekable-format-go/pkg"
)

type readCloser struct {
    io.Reader
    io.Closer
}

func main() {
    ctx := context.Background()

    var (
        inputFlag, chunkingFlag, outputFlag string
        qualityFlag                         int
        verifyFlag, verboseFlag             bool
    )

    flag.StringVar(&inputFlag, "f", "", "input filename")
    flag.StringVar(&outputFlag, "o", "", "output filename")
    flag.StringVar(&chunkingFlag, "c", "128:1024:8192", "min:avg:max chunking block size (in kb)")
    flag.BoolVar(&verifyFlag, "t", false, "test reading after the write")
    flag.IntVar(&qualityFlag, "q", 1, "compression quality (lower == faster)")
    flag.BoolVar(&verboseFlag, "v", false, "be verbose")

    flag.Parse()

    var err error
    var logger *zap.Logger
    if verboseFlag {
        logger, err = zap.NewDevelopment()
    } else {
        logger, err = zap.NewProduction()
    }
    if err != nil {
        log.Fatal("failed to initialize logger", err)
    }
    defer func() {
        _ = logger.Sync()
    }()

    if inputFlag == "" || outputFlag == "" {
        logger.Fatal("both input and output files need to be defined")
    }
    if verifyFlag && outputFlag == "-" {
        logger.Fatal("verify can't be used with stdout output")
    }

    bar := progressbar.DefaultSilent(0, "")

    inputFile := os.Stdin
    if inputFlag != "-" {
        if inputFile, err = os.Open(inputFlag); err != nil {
            logger.Fatal("failed to open input", zap.Error(err))
        }

        if term.IsTerminal(int(os.Stdout.Fd())) {
            size := int64(-1)
            stat, err := inputFile.Stat()
            if err == nil {
                size = stat.Size()
            }

            bar = progressbar.DefaultBytes(
                size,
                "compressing",
            )
        }
    }

    var input io.ReadCloser = inputFile

    expected := sha512.New512_256()
    origDone := make(chan struct{})
    if verifyFlag {
        pr, pw := io.Pipe()

        tee := io.TeeReader(inputFile, pw)
        input = readCloser{tee, pw}

        go func() {
            defer close(origDone)

            m, err := io.CopyBuffer(expected, pr, make([]byte, 128<<10))
            if err != nil {
                logger.Fatal("failed to compute expected csum", zap.Int64("processed", m), zap.Error(err))
            }
        }()
    }

    output := os.Stdout
    if outputFlag != "-" {
        output, err = os.OpenFile(outputFlag, os.O_TRUNC|os.O_WRONLY|os.O_CREATE, 0o644)
        if err != nil {
            logger.Fatal("failed to open output", zap.Error(err))
        }
        defer output.Close()
    }

    chunkParams := strings.Split(chunkingFlag, ":")
    if len(chunkParams) != 3 {
        logger.Fatal("failed parse chunker params. len() != 3", zap.Int("actual", len(chunkParams)))
    }
    mustConv := func(s string) int {
        n, err := strconv.Atoi(s)
        if err != nil {
            logger.Fatal("failed to parse int", zap.String("string", s), zap.Error(err))
        }
        return n
    }
    minChunkSize := mustConv(chunkParams[0]) * 1024
    avgChunkSize := mustConv(chunkParams[1]) * 1024
    maxChunkSize := mustConv(chunkParams[2]) * 1024

    var zstdOpts []zstd.EOption = []zstd.EOption{
        zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(qualityFlag)),
    }
    enc, err := zstd.NewWriter(nil, zstdOpts...)
    if err != nil {
        logger.Fatal("failed to create zstd encoder", zap.Error(err))
    }

    w, err := seekable.NewWriter(output, enc, seekable.WithWLogger(logger))
    if err != nil {
        logger.Fatal("failed to create compressed writer", zap.Error(err))
    }
    defer w.Close()

    // convert average chunk size to a number of bits
    logger.Debug("setting chunker params", zap.Int("min", minChunkSize), zap.Int("max", maxChunkSize))
    chunker, err := fastcdc.NewChunker(
        input,
        fastcdc.Options{
            MinSize:     minChunkSize,
            AverageSize: avgChunkSize,
            MaxSize:     maxChunkSize,
        },
    )
    if err != nil {
        logger.Fatal("failed to create chunker", zap.Error(err))
    }

    // Target frame size: 4MiB
    const targetFrameSize = 4 * 1024 * 1024 // 4MiB

    frameSource := func() ([]byte, error) {
        var frameBuffer bytes.Buffer

        // Accumulate chunks until we reach target frame size
        for frameBuffer.Len() < targetFrameSize {
            chunk, err := chunker.Next()
            if err != nil {
                if errors.Is(err, io.EOF) {
                    // Return any remaining data in buffer
                    if frameBuffer.Len() > 0 {
                        return frameBuffer.Bytes(), nil
                    }
                    return nil, nil
                }
                return nil, err
            }

            // Add chunk data to frame buffer
            frameBuffer.Write(chunk.Data)
        }

        return frameBuffer.Bytes(), nil
    }

    err = w.WriteMany(ctx, frameSource, seekable.WithWriteCallback(func(size uint32) {
        _ = bar.Add(int(size))
    }))
    if err != nil {
        logger.Fatal("failed to write data", zap.Error(err))
    }

    _ = bar.Finish()
    input.Close()
    w.Close()

    if verifyFlag {
        logger.Info("verifying checksum")

        verify, err := os.Open(outputFlag)
        if err != nil {
            logger.Fatal("failed to open file for verification", zap.Error(err))
        }
        defer verify.Close()

        dec, err := zstd.NewReader(nil)
        if err != nil {
            logger.Fatal("failed to create zstd decompressor", zap.Error(err))
        }
        defer dec.Close()

        reader, err := seekable.NewReader(verify, dec, seekable.WithRLogger(logger))
        if err != nil {
            logger.Fatal("failed to create new seekable reader", zap.Error(err))
        }

        actual := sha512.New512_256()
        m, err := io.CopyBuffer(actual, reader, make([]byte, 128<<10))
        if err != nil {
            logger.Fatal("failed to compute actual csum", zap.Int64("processed", m), zap.Error(err))
        }
        <-origDone

        if !bytes.Equal(actual.Sum(nil), expected.Sum(nil)) {
            logger.Fatal("checksum verification failed",
                zap.Binary("actual", actual.Sum(nil)), zap.Binary("expected", expected.Sum(nil)))
        } else {
            logger.Info("checksum verification succeeded", zap.Binary("actual", actual.Sum(nil)))
        }
    }
}