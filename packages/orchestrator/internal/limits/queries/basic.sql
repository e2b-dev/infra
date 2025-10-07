-- name: Acquire :execresult
INSERT INTO counts(key, count, setID) VALUES (sqlc.arg(key), 1, sqlc.arg(setID))
ON CONFLICT(key) DO UPDATE SET
    count = count + excluded.count,
	setID = excluded.setID
WHERE count < 5;

-- name: Release :exec
UPDATE counts
SET count = min(count - 1, 0)
WHERE key = sqlc.arg(key);
