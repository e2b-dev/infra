package storage

// func DownloadFrames(ctx context.Context, src RangeGetter, start int64, n int, ft *FrameTable) (int64, [][]byte, error) {
// 	// TODO add a max concurrency limiter for frame downloads
// 	// TODO consider downloading multiple frames per request if too many frames?
// 	nFrames := 0
// 	err := ft.Range(start, int64(n), func(off Offset, frame FrameSize) error {
// 		nFrames++
// 		return nil
// 	})
// 	if err != nil {
// 		return 0, nil, err
// 	}

// 	all := make([][]byte, nFrames)
// 	frameIndex := 0
// 	eg, ctx := errgroup.WithContext(ctx)
// 	var framesStart int64 = -1
// 	err = ft.Range(start, int64(n), func(off Offset, frame FrameSize) error {
// 		i := frameIndex
// 		frameIndex++

// 		fmt.Printf("<>/<> Download: frame %d at offset %#x/%#x (%#x/%#x bytes)\n", i, off.U, frame.U, off.C, frame.C) // --- IGNORE ---

// 		if framesStart == -1 {
// 			framesStart = off.U
// 		}

// 		eg.Go(func() error {
// 			fbuf, err := downloadFrame(ctx, src, off, frame)
// 			if err != nil {
// 				return fmt.Errorf("failed to read compressed frame at offset %d: %w", off.C, err)
// 			}
// 			all[i] = fbuf

// 			return nil
// 		})

// 		return nil
// 	})
// 	if err != nil {
// 		return 0, nil, err
// 	}

// 	if err = eg.Wait(); err != nil {
// 		return 0, nil, err
// 	}

// 	return framesStart, all, nil
// }

// func downloadFrame(ctx context.Context, src RangeGetter, frameOffset Offset, frame FrameSize) ([]byte, error) {
// 	// r reads the compressed frame from GCS
// 	r, err := src.RangeGet(ctx, frameOffset.C, int64(frame.C))
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to create a range reader: %w", err)
// 	}
// 	defer r.Close()

// 	// TODO recycle decoders?
// 	dec, err := zstd.NewReader(r)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
// 	}
// 	defer dec.Close()

// 	buf := bytes.NewBuffer(make([]byte, 0, frame.U))
// 	_, err = io.Copy(buf, dec)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to read decompressed data: %w", err)
// 	}

// 	return buf.Bytes(), nil
// }
