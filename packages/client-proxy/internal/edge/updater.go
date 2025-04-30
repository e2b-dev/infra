package edge

//var (
//	autoUpdateLastSum *string = nil
//)
//
//func autoUpdate(updateSourceUrl string, interval time.Duration) {
//	updateTicker := time.NewTicker(interval)
//
//	for {
//		select {
//		case <-updateTicker.C:
//			zap.L().Info("checking for update", zap.String("url", updateSourceUrl))
//
//			resp, err := http.Get(updateSourceUrl)
//			if err != nil {
//				zap.L().Error("failed to get update", zap.Error(err))
//				continue
//			}
//			defer resp.Body.Close()
//
//			if resp.StatusCode != http.StatusOK {
//				zap.L().Error("failed to get update", zap.Int("status", resp.StatusCode))
//				continue
//			}
//
//			updateBytes, err := io.ReadAll(resp.Body)
//			if err != nil {
//				zap.L().Error("failed to read update body", zap.Error(err))
//				continue
//			}
//
//			updateHashRaw := sha256.Sum256(updateBytes)
//			updateHash := fmt.Sprintf("%x", updateHashRaw)
//
//			// okay, first round
//			if autoUpdateLastSum == nil {
//				autoUpdateLastSum = &updateHash
//				zap.L().Info("first update", zap.String("hash", updateHash))
//				continue
//			}
//
//			if updateHash == *autoUpdateLastSum {
//				zap.L().Debug("no update available")
//				continue
//			}
//
//			zap.L().Info("update available", zap.String("hash", updateHash))
//
//			copied := make([]byte, len(updateBytes))
//			copy(copied, updateBytes)
//
//			err = selfupdate.Apply(bytes.NewReader(copied), selfupdate.Options{})
//			if err != nil {
//				zap.L().Error("failed to apply update", zap.Error(err))
//			} else {
//				zap.L().Info("update applied successfully, stopping service")
//				autoUpdateLastSum = &updateHash
//
//				// todo: okay this is not pretty, but we can do it better way
//				os.Exit(1)
//				return
//			}
//		}
//	}
//}
//
