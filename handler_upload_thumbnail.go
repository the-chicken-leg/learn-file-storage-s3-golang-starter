package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)
	formFile, formFileHeader, err := r.FormFile("thumbnail")
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
	if mediatype != "image/jpeg" && mediatype != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Must be jpeg or png", errors.New("Must be jpeg or png"))
		return
	}

	// save thumbnail to filesystem
	fileName := dbVideo.ID.String() + "." + strings.Split(mediatype, "/")[1]
	thumbnailPath := filepath.Join(cfg.assetsRoot, fileName)
	thumbnailFile, err := os.Create(thumbnailPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not create thumbnail file", err)
		return
	}
	if _, err = io.Copy(thumbnailFile, formFile); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not write to thumbnail file", err)
		return
	}

	// update thumbnail URL in db
	thumbnailURL := fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, fileName)
	dbVideo.ThumbnailURL = &thumbnailURL
	if cfg.db.UpdateVideo(dbVideo); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not update thumbnail URL in db", err)
		return
	}

	// success
	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)
	respondWithJSON(w, http.StatusOK, dbVideo)
}
