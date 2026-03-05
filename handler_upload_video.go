package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	// check if video db entry exists
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}
	dbVideo, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}

	// check auth
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}
	if userID != dbVideo.UserID {
		respondWithError(w, http.StatusUnauthorized, "Video does not belong to user", errors.New("Video does not belong to user"))
		return
	}

	// parse request
	r.Body = http.MaxBytesReader(w, r.Body, 1 << 30)
	formFile, formFileHeader, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer formFile.Close()

	// parse media type
	mediatype, _, err := mime.ParseMediaType(formFileHeader.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not parse media type", err)
		return
	}
	if mediatype != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Must be mp4", errors.New("Must be mp4"))
		return
	}

	// save temp video to filesystem
	tempFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not create temp video file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()
	if _, err = io.Copy(tempFile, formFile); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not write to temp video file", err)
		return
	}
	tempFile.Seek(0, io.SeekStart)
	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error calculating aspect ratio", err)
		return
	}

	// upload to S3
	rando := make([]byte, 32)
	_, _ = rand.Read(rando)
	fileStem := base64.RawURLEncoding.EncodeToString(rando)
	fileName := aspectRatio + "/" + fileStem + "." + strings.Split(mediatype, "/")[1]
	_, err = cfg.s3Client.PutObject(
		r.Context(),
		&s3.PutObjectInput{
			Bucket: &cfg.s3Bucket,
			Key: &fileName,
			Body: tempFile,
			ContentType: &mediatype,
		},
	)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading file to S3", err)
		return
	}

	// update video URL in db
	videoURL := fmt.Sprintf("http://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileName)
	dbVideo.VideoURL = &videoURL
	if err = cfg.db.UpdateVideo(dbVideo); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not update video URL in db", err)
		return
	}

	// success
	fmt.Println("uploading video", videoID, "by user", userID)
	respondWithJSON(w, http.StatusOK, dbVideo)
}

func getVideoAspectRatio(filePath string) (string, error) {
	type widthHeightOut struct {
		Streams []struct {
			Width              int    `json:"width"`
			Height             int    `json:"height"`
		}
	}
	
	ffprobeCmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_streams", filePath,
	)
	var buf bytes.Buffer
	ffprobeCmd.Stdout = &buf
	if err := ffprobeCmd.Run(); err != nil {
		return "", fmt.Errorf("ffprobe command unsuccessful: %v", err)
	}

	var ffprobeOut widthHeightOut
	if err := json.Unmarshal(buf.Bytes(), &ffprobeOut); err != nil {
		return "", fmt.Errorf("Could not unmarshal JSON to struct: %v", err)
	}

	aspectRatio := ffprobeOut.Streams[0].Width / ffprobeOut.Streams[0].Height
	switch aspectRatio {
	case 1:
		return "landscape", nil
	case 0:
		return "portrait", nil
	default:
		return "other", nil
	}
}
