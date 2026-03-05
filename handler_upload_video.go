package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
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

	// upload to S3
	rando := make([]byte, 32)
	_, _ = rand.Read(rando)
	fileStem := base64.RawURLEncoding.EncodeToString(rando)
	fileName := fileStem + "." + strings.Split(mediatype, "/")[1]
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
