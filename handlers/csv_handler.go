package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/storage"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/option"

	"adfit-oauth/config"
)

type ValidationResult struct {
	Success     bool
	Message     string
	FileCount   int
	Files       []string
	TableDataOK bool
	MissingCols []string
	Details     map[string]interface{}
}

// CSVHandler - CSV 파일 검증 및 저장 핸들러
type CSVHandler struct {
	firestore     *firestore.Client
	storageClient *storage.Client
	bucketName    string
}

// NewCSVHandler - CSVHandler 생성자
func NewCSVHandler(bucketName string) (*CSVHandler, error) {
	ctx := context.Background()

	// Firestore 초기화
	var firestoreClient *firestore.Client
	var err error

	if config.Config != nil && config.Config.Firebase.CredentialsPath != "" {
		firestoreClient, err = firestore.NewClient(ctx, config.Config.Firebase.ProjectID,
			option.WithCredentialsFile(config.Config.Firebase.CredentialsPath))
	} else {
		firestoreClient, err = firestore.NewClient(ctx, "posted-app-c4ff5")
	}

	if err != nil {
		return nil, fmt.Errorf("Firestore 초기화 실패: %v", err)
	}

	// Storage 초기화
	var storageClient *storage.Client
	if config.Config != nil && config.Config.Firebase.CredentialsPath != "" {
		storageClient, err = storage.NewClient(ctx,
			option.WithCredentialsFile(config.Config.Firebase.CredentialsPath))
	} else {
		storageClient, err = storage.NewClient(ctx)
	}

	if err != nil {
		return nil, fmt.Errorf("Storage 초기화 실패: %v", err)
	}

	return &CSVHandler{
		firestore:     firestoreClient,
		storageClient: storageClient,
		bucketName:    bucketName,
	}, nil
}

// ValidateAndSaveFile1 - 파일 1 검증, 파싱 및 저장 (지역 데이터)
func (h *CSVHandler) ValidateAndSaveFile1(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(401, gin.H{
			"success": false,
			"message": "인증이 필요합니다",
			"error":   "NO_USER_ID",
		})
		return
	}

	// Form 데이터 읽기
	competitionID := c.PostForm("competitionId")
	brandID := c.PostForm("brandId")
	submissionID := c.PostForm("submissionId") // ⭐ 추가
	videoIndex := c.PostForm("videoIndex")     // ⭐ 추가 (1, 2, 3)

	if competitionID == "" || brandID == "" || submissionID == "" || videoIndex == "" {
		c.JSON(400, gin.H{
			"success": false,
			"message": "competitionId, brandId, submissionId, videoIndex가 필요합니다",
			"error":   "MISSING_REQUIRED_FIELDS",
		})
		return
	}

	// Multipart 파일 읽기
	file, header, err := c.Request.FormFile("zipfile")
	if err != nil {
		c.JSON(400, gin.H{
			"success": false,
			"message": "ZIP 파일이 필요합니다",
			"error":   "NO_FILE",
		})
		return
	}
	defer file.Close()

	// 파일 내용을 메모리에 읽기
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "파일 읽기 실패",
			"error":   err.Error(),
		})
		return
	}

	// Context 생성
	ctx := context.Background()

	// 검증 및 파싱 실행
	log.Printf("📦 파일 1 검증 시작: %s (사용자: %s, 대회: %s, 제출: %s, 비디오: %s)", header.Filename, userID, competitionID, submissionID, videoIndex)
	result := validateZipFile1FromBytes(fileBytes)

	if !result.Success {
		c.JSON(400, gin.H{
			"success": false,
			"message": result.Message,
			"error":   "VALIDATION_FAILED",
			"details": gin.H{
				"fileCount":   result.FileCount,
				"files":       result.Files,
				"missingCols": result.MissingCols,
			},
		})
		return
	}

	// ⭐ 지역 데이터 파싱
	log.Printf("📊 지역 데이터 파싱 시작")
	geoData, isEmpty, err := parseGeographyDataFromBytes(fileBytes)
	if err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "지역 데이터 파싱 실패",
			"error":   err.Error(),
		})
		return
	}

	// Storage에 업로드 (⭐ 사용자별 폴더 구분)
	timestamp := time.Now().Unix()
	// 경로: brands/{brandId}/competitions/{competitionId}/analytics/{userId}/vd{videoIndex}_file1_{timestamp}.zip
	storagePath := fmt.Sprintf("brands/%s/competitions/%s/analytics/%s/vd%s_file1_%d.zip",
		brandID, competitionID, userID, videoIndex, timestamp)
	storageURL := fmt.Sprintf("gs://%s/%s", h.bucketName, storagePath)

	// 원본 파일명 저장
	originalFileName := header.Filename

	log.Printf("☁️ Storage 업로드: %s", storagePath)
	bucket := h.storageClient.Bucket(h.bucketName)
	obj := bucket.Object(storagePath)
	w := obj.NewWriter(ctx)

	if _, err := w.Write(fileBytes); err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "Storage 업로드 실패",
			"error":   err.Error(),
		})
		return
	}

	if err := w.Close(); err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "Storage 업로드 완료 실패",
			"error":   err.Error(),
		})
		return
	}

	// Firestore 메타데이터 저장 (⭐ submissionId 사용)
	// submissions에서 videoId 가져오기
	log.Printf("🔍 제출 정보 조회: competitions/%s/submissions/%s", competitionID, submissionID)
	submissionDoc, err := h.firestore.Collection("competitions").Doc(competitionID).
		Collection("submissions").Doc(submissionID).Get(ctx)
	if err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "제출 정보 조회 실패",
			"error":   err.Error(),
		})
		return
	}

	submissionData := submissionDoc.Data()

	// youtubeData.id에서 videoId 추출
	var videoID string
	if youtubeData, ok := submissionData["youtubeData"].(map[string]interface{}); ok {
		if vid, ok := youtubeData["id"].(string); ok && vid != "" {
			videoID = vid
			log.Printf("📍 youtubeData.id에서 videoId 추출: %s", videoID)
		}
	}

	if videoID == "" {
		c.JSON(400, gin.H{
			"success": false,
			"message": "youtubeData.id를 찾을 수 없습니다",
			"error":   "NO_VIDEO_ID",
		})
		return
	}

	// ⭐ 데이터가 있는 경우만 analytics에 저장
	now := time.Now()
	if !isEmpty {
		log.Printf("💾 지역 통계 저장: competitions/%s/analytics/videos/list/%s", competitionID, videoID)
		videoStatsRef := h.firestore.Collection("competitions").Doc(competitionID).
			Collection("analytics").Doc("videos").Collection("list").Doc(videoID)

		_, err = videoStatsRef.Set(ctx, map[string]interface{}{
			"submissionId": submissionID,
			"videoId":      videoID,
			"geography":    geoData,
			"metadata": map[string]interface{}{
				"hasGeography": true,
				"lastUpdated":  now,
			},
		}, firestore.MergeAll)

		if err != nil {
			c.JSON(500, gin.H{
				"success": false,
				"message": "지역 통계 저장 실패",
				"error":   err.Error(),
			})
			return
		}
	} else {
		log.Printf("⚠️ 데이터가 없어 analytics에 저장하지 않음")
	}

	// submissions에 파일 정보 저장
	log.Printf("💾 제출 문서 업데이트: competitions/%s/submissions/%s", competitionID, submissionID)
	updateData := map[string]interface{}{
		"file1Url":          storageURL,
		"file1OriginalName": originalFileName,
		"file1Validated":    true,
		"file1UploadedAt":   now,
		"file1Files":        result.Files,
		"file1VideoIndex":   videoIndex,
		"file1IsEmpty":      isEmpty, // ⭐ 데이터 유무 표시
		"updatedAt":         now,
	}

	_, err = h.firestore.Collection("competitions").Doc(competitionID).
		Collection("submissions").Doc(submissionID).Set(ctx, updateData, firestore.MergeAll)

	if err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "제출 문서 업데이트 실패",
			"error":   err.Error(),
		})
		return
	}

	// ⭐ 대회 전체 통계 자동 집계
	log.Printf("📊 대회 전체 통계 자동 집계 시작")
	err = h.aggregateCompetitionStats(ctx, competitionID)
	if err != nil {
		log.Printf("⚠️ 통계 집계 실패 (파일 저장은 성공): %v", err)
	}

	log.Printf("✅ 파일 1 처리 완료 (지역 데이터)")
	
	// ⭐ 응답 메시지 구성
	var responseMsg string
	responseStats := gin.H{}
	
	if isEmpty {
		responseMsg = "파일 제출 완료 (데이터 없음 - 조회수가 충분하지 않은 것으로 보입니다)"
		responseStats["isEmpty"] = true
	} else {
		responseMsg = "지역 데이터 업로드 완료"
		responseStats["totalCountries"] = geoData["totalCountries"]
		responseStats["totalViews"] = geoData["totalViews"]
	}
	
	c.JSON(200, gin.H{
		"success": true,
		"message": responseMsg,
		"stats":   responseStats,
	})
}

// ValidateAndSaveFile2 - 파일 2 검증, 파싱 및 저장 (연령&성별 데이터)
func (h *CSVHandler) ValidateAndSaveFile2(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(401, gin.H{
			"success": false,
			"message": "인증이 필요합니다",
			"error":   "NO_USER_ID",
		})
		return
	}

	// Form 데이터 읽기
	competitionID := c.PostForm("competitionId")
	brandID := c.PostForm("brandId")
	submissionID := c.PostForm("submissionId") // ⭐ 추가
	videoIndex := c.PostForm("videoIndex")     // ⭐ 추가 (1, 2, 3)

	if competitionID == "" || brandID == "" || submissionID == "" || videoIndex == "" {
		c.JSON(400, gin.H{
			"success": false,
			"message": "competitionId, brandId, submissionId, videoIndex가 필요합니다",
			"error":   "MISSING_REQUIRED_FIELDS",
		})
		return
	}

	// Multipart 파일 읽기
	file, header, err := c.Request.FormFile("zipfile")
	if err != nil {
		c.JSON(400, gin.H{
			"success": false,
			"message": "ZIP 파일이 필요합니다",
			"error":   "NO_FILE",
		})
		return
	}
	defer file.Close()

	// 파일 내용을 메모리에 읽기
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "파일 읽기 실패",
			"error":   err.Error(),
		})
		return
	}

	// 검증 및 파싱 실행
	log.Printf("📦 파일 2 검증 시작: %s (사용자: %s, 대회: %s, 제출: %s, 비디오: %s)", header.Filename, userID, competitionID, submissionID, videoIndex)
	result := validateZipFile2FromBytes(fileBytes)

	if !result.Success {
		c.JSON(400, gin.H{
			"success": false,
			"message": result.Message,
			"error":   "VALIDATION_FAILED",
			"details": gin.H{
				"fileCount":   result.FileCount,
				"files":       result.Files,
				"missingCols": result.MissingCols,
			},
		})
		return
	}

	ctx := context.Background()

	// submissions에서 videoId와 currentViewCount 가져오기
	log.Printf("🔍 제출 정보 조회: competitions/%s/submissions/%s", competitionID, submissionID)
	submissionDoc, err := h.firestore.Collection("competitions").Doc(competitionID).
		Collection("submissions").Doc(submissionID).Get(ctx)
	if err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "제출 정보 조회 실패",
			"error":   err.Error(),
		})
		return
	}

	submissionData := submissionDoc.Data()

	// youtubeData.id에서 videoId 추출
	var videoID string
	if youtubeData, ok := submissionData["youtubeData"].(map[string]interface{}); ok {
		if vid, ok := youtubeData["id"].(string); ok && vid != "" {
			videoID = vid
			log.Printf("📍 youtubeData.id에서 videoId 추출: %s", videoID)
		}
	}

	if videoID == "" {
		c.JSON(400, gin.H{
			"success": false,
			"message": "youtubeData.id를 찾을 수 없습니다",
			"error":   "NO_VIDEO_ID",
		})
		return
	}

	// currentViewCount 가져오기
	var baseViews int64
	if viewCount, ok := submissionData["currentViewCount"].(int64); ok {
		baseViews = viewCount
	} else if viewCount, ok := submissionData["viewCount"].(int64); ok {
		baseViews = viewCount
	} else {
		c.JSON(400, gin.H{
			"success": false,
			"message": "조회수 정보를 찾을 수 없습니다",
			"error":   "NO_VIEW_COUNT",
		})
		return
	}

	if baseViews == 0 {
		c.JSON(400, gin.H{
			"success": false,
			"message": "조회수가 0입니다. 연령&성별 데이터를 계산할 수 없습니다.",
			"error":   "ZERO_VIEW_COUNT",
		})
		return
	}

	// ⭐ 연령&성별 데이터 파싱 (baseViews 기반 계산)
	log.Printf("📊 연령&성별 데이터 파싱 시작 (기준 조회수: %d)", baseViews)
	demoData, isEmpty, err := parseDemographicsDataFromBytes(fileBytes, int(baseViews))
	if err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "연령&성별 데이터 파싱 실패",
			"error":   err.Error(),
		})
		return
	}

	// Storage에 업로드 (⭐ 사용자별 폴더 구분)
	timestamp := time.Now().Unix()
	// 경로: brands/{brandId}/competitions/{competitionId}/analytics/{userId}/vd{videoIndex}_file2_{timestamp}.zip
	storagePath := fmt.Sprintf("brands/%s/competitions/%s/analytics/%s/vd%s_file2_%d.zip",
		brandID, competitionID, userID, videoIndex, timestamp)
	storageURL := fmt.Sprintf("gs://%s/%s", h.bucketName, storagePath)

	// 원본 파일명 저장
	originalFileName := header.Filename

	log.Printf("☁️ Storage 업로드: %s", storagePath)
	bucket := h.storageClient.Bucket(h.bucketName)
	obj := bucket.Object(storagePath)
	w := obj.NewWriter(ctx)

	if _, err := w.Write(fileBytes); err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "Storage 업로드 실패",
			"error":   err.Error(),
		})
		return
	}

	if err := w.Close(); err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "Storage 업로드 완료 실패",
			"error":   err.Error(),
		})
		return
	}

	// Firestore 메타데이터 저장 (⭐ submissionId 사용)
	now := time.Now()
	
	// ⭐ 데이터가 있는 경우만 analytics에 저장
	if !isEmpty {
		log.Printf("💾 연령&성별 통계 저장: competitions/%s/analytics/videos/list/%s", competitionID, videoID)
		videoStatsRef := h.firestore.Collection("competitions").Doc(competitionID).
			Collection("analytics").Doc("videos").Collection("list").Doc(videoID)

		_, err = videoStatsRef.Set(ctx, map[string]interface{}{
			"submissionId": submissionID,
			"videoId":      videoID,
			"demographics": demoData,
			"metadata": map[string]interface{}{
				"hasDemographics": true,
				"lastUpdated":     now,
			},
		}, firestore.MergeAll)

		if err != nil {
			c.JSON(500, gin.H{
				"success": false,
				"message": "연령&성별 통계 저장 실패",
				"error":   err.Error(),
			})
			return
		}
	} else {
		log.Printf("⚠️ 데이터가 없어 analytics에 저장하지 않음")
	}

	// submissions에 파일 정보 저장
	log.Printf("💾 제출 문서 업데이트: competitions/%s/submissions/%s", competitionID, submissionID)
	updateData := map[string]interface{}{
		"file2Url":          storageURL,
		"file2OriginalName": originalFileName,
		"file2Validated":    true,
		"file2UploadedAt":   now,
		"file2Details":      result.Details,
		"file2VideoIndex":   videoIndex,
		"file2IsEmpty":      isEmpty,  // ⭐ 데이터 유무 표시
		"updatedAt":         now,
	}

	_, err = h.firestore.Collection("competitions").Doc(competitionID).
		Collection("submissions").Doc(submissionID).Set(ctx, updateData, firestore.MergeAll)

	if err != nil {
		c.JSON(500, gin.H{
			"success": false,
			"message": "제출 문서 업데이트 실패",
			"error":   err.Error(),
		})
		return
	}

	// ⭐ 대회 전체 통계 자동 집계
	log.Printf("📊 대회 전체 통계 자동 집계 시작")
	err = h.aggregateCompetitionStats(ctx, competitionID)
	if err != nil {
		log.Printf("⚠️ 통계 집계 실패 (파일 저장은 성공): %v", err)
	}

	log.Printf("✅ 파일 2 처리 완료 (연령&성별 데이터)")
	
	// ⭐ 응답 메시지 구성
	var responseMsg string
	responseStats := gin.H{}
	
	if isEmpty {
		responseMsg = "파일 제출 완료 (데이터 없음 - 조회수가 충분하지 않은 것으로 보입니다)"
		responseStats["isEmpty"] = true
	} else {
		responseMsg = fmt.Sprintf("연령&성별 데이터 업로드 완료 (기준 조회수: %s)", formatNumber(int(baseViews)))
		responseStats["baseViews"] = baseViews
		responseStats["genderCount"] = len(demoData["gender"].(map[string]float64))
		responseStats["ageCount"] = len(demoData["ageGroup"].(map[string]float64))
	}
	
	c.JSON(200, gin.H{
		"success": true,
		"message": responseMsg,
		"stats":   responseStats,
	})
}

// validateZipFile1FromBytes - 첫 번째 파일 검증 (메모리에서)
func validateZipFile1FromBytes(fileBytes []byte) ValidationResult {
	reader, err := zip.NewReader(bytes.NewReader(fileBytes), int64(len(fileBytes)))
	if err != nil {
		return ValidationResult{
			Success: false,
			Message: fmt.Sprintf("ZIP 파일 열기 실패: %v", err),
			Files:   []string{},
			Details: make(map[string]interface{}),
		}
	}
	return validateZipFile1Internal(reader.File)
}

// validateZipFile1Internal - 첫 번째 파일 검증 (내부 로직)
func validateZipFile1Internal(files []*zip.File) ValidationResult {
	result := ValidationResult{
		Success: false,
		Files:   []string{},
		Details: make(map[string]interface{}),
	}

	// CSV 파일만 필터링
	var csvFiles []*zip.File
	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
			fileName := extractFileName(f.Name)
			if fileName != "" {
				csvFiles = append(csvFiles, f)
				result.Files = append(result.Files, fileName)
			}
		}
	}

	result.FileCount = len(csvFiles)

	// ✅ 검증 1: CSV 파일 개수 (3개)
	log.Printf("🔍 검증 1: CSV 파일 개수 확인")
	log.Printf("   발견된 CSV 파일: %d개", result.FileCount)
	for i, name := range result.Files {
		log.Printf("   %d. %s", i+1, name)
	}

	if len(csvFiles) != 3 {
		result.Message = fmt.Sprintf("CSV 파일이 3개가 아닙니다 (현재: %d개)", len(csvFiles))
		return result
	}
	log.Printf("   ✅ CSV 파일 3개 확인 완료")

	// ✅ 검증 2: 표 데이터 찾기 (지역, 조회수 컬럼)
	log.Printf("🔍 검증 2: 표 데이터 컬럼 확인 (지역/Geography, 조회수/Views)")

	tableDataFound := false
	var tableFileName string

	for _, csvFile := range csvFiles {
		fileName := extractFileName(csvFile.Name)
		// "표 데이터" 또는 "Chart" 포함 파일만 검사
		if !strings.Contains(fileName, "표 데이터") && !strings.Contains(fileName, "Chart") {
			continue
		}

		rc, err := csvFile.Open()
		if err != nil {
			continue
		}

		reader := csv.NewReader(rc)
		headers, err := reader.Read()
		rc.Close()

		if err != nil {
			continue
		}

		hasRegion := false
		hasViewCount := false

		log.Printf("   📄 %s 컬럼: ", fileName)
		for i, header := range headers {
			trimmed := strings.TrimSpace(header)
			if i > 0 {
				log.Print(", ")
			}
			log.Printf("'%s'", trimmed)

			// 지역 검사 (한글 또는 영어)
			if trimmed == "지역" || trimmed == "Geography" {
				hasRegion = true
			}
			// 조회수 검사 (한글 또는 영어)
			if trimmed == "조회수" || trimmed == "Views" {
				hasViewCount = true
			}
		}

		if hasRegion && hasViewCount {
			tableDataFound = true
			tableFileName = fileName
			result.TableDataOK = true
			log.Printf("      ✅ '지역/Geography', '조회수/Views' 컬럼 발견!")
			break
		}
	}

	if !tableDataFound {
		result.Message = "표 데이터 또는 Chart 파일에 '지역/Geography', '조회수/Views' 컬럼이 없습니다"
		result.TableDataOK = false
		result.MissingCols = []string{"지역/Geography", "조회수/Views"}
		return result
	}

	result.Success = true
	result.Message = fmt.Sprintf("검증 성공: CSV 3개, 표 데이터(%s) 확인 완료", tableFileName)

	return result
}

// validateZipFile2FromBytes - 두 번째 파일 검증 (메모리에서)
func validateZipFile2FromBytes(fileBytes []byte) ValidationResult {
	reader, err := zip.NewReader(bytes.NewReader(fileBytes), int64(len(fileBytes)))
	if err != nil {
		return ValidationResult{
			Success: false,
			Message: fmt.Sprintf("ZIP 파일 열기 실패: %v", err),
			Files:   []string{},
			Details: make(map[string]interface{}),
		}
	}
	return validateZipFile2Internal(reader.File)
}

// validateZipFile2Internal - 두 번째 파일 검증 (내부 로직)
func validateZipFile2Internal(files []*zip.File) ValidationResult {
	result := ValidationResult{
		Success: false,
		Files:   []string{},
		Details: make(map[string]interface{}),
	}

	// CSV 파일만 필터링
	var csvFiles []*zip.File
	var tableDataFile *zip.File

	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
			fileName := extractFileName(f.Name)
			if fileName != "" {
				csvFiles = append(csvFiles, f)
				result.Files = append(result.Files, fileName)

				// "표 데이터" 또는 "Chart" 포함 파일 찾기
				if strings.Contains(fileName, "표 데이터") || strings.Contains(fileName, "Chart") {
					tableDataFile = f
				}
			}
		}
	}

	result.FileCount = len(csvFiles)

	// ✅ 검증 1: "표 데이터" 또는 "Chart" 파일 존재 확인
	log.Printf("🔍 검증 1: '표 데이터' 또는 'Chart' 파일 존재 확인")
	log.Printf("   발견된 CSV 파일: %d개", result.FileCount)
	for i, name := range result.Files {
		log.Printf("   %d. %s", i+1, name)
	}

	if tableDataFile == nil {
		result.Message = "'표 데이터' 또는 'Chart' 파일이 없습니다"
		return result
	}
	log.Printf("   ✅ '표 데이터/Chart' 파일 확인 완료")

	// CSV 파일 읽기
	rc, err := tableDataFile.Open()
	if err != nil {
		result.Message = "표 데이터.csv 파일을 열 수 없습니다"
		return result
	}
	defer rc.Close()

	csvReader := csv.NewReader(rc)
	records, err := csvReader.ReadAll()
	if err != nil {
		result.Message = "CSV 파일 읽기 실패"
		return result
	}

	if len(records) < 1 {
		result.Message = "CSV 파일이 비어있습니다"
		return result
	}

	headers := records[0]
	_ = records[1:] // dataRows는 검증 4, 5에서 사용 (현재 주석처리됨)

	// ✅ 검증 2: 컬럼 개수 (4개)
	log.Printf("🔍 검증 2: 컬럼 개수 확인 (4개)")
	log.Printf("   발견된 컬럼 개수: %d개", len(headers))
	log.Printf("   컬럼: ")
	for i, header := range headers {
		if i > 0 {
			log.Print(", ")
		}
		log.Printf("'%s'", strings.TrimSpace(header))
	}

	if len(headers) != 4 {
		result.Message = fmt.Sprintf("컬럼이 4개가 아닙니다 (현재: %d개)", len(headers))
		return result
	}
	log.Printf("   ✅ 컬럼 4개 확인 완료")

	// ✅ 검증 3: 필수 컬럼 (시청자 연령, 시청자 성별)
	log.Printf("🔍 검증 3: 필수 컬럼 확인 (시청자 연령/Viewer age, 시청자 성별/Viewer gender)")

	hasAge := false
	hasGender := false
	viewCountColIdx := -1
	watchTimeColIdx := -1

	for i, header := range headers {
		trimmed := strings.TrimSpace(header)

		// 연령 검사 (한글 또는 영어)
		if trimmed == "시청자 연령" || trimmed == "Viewer age" {
			hasAge = true
			log.Printf("   ✅ '%s' 컬럼 발견 (위치: %d)", trimmed, i+1)
		}
		// 성별 검사 (한글 또는 영어)
		if trimmed == "시청자 성별" || trimmed == "Viewer gender" {
			hasGender = true
			log.Printf("   ✅ '%s' 컬럼 발견 (위치: %d)", trimmed, i+1)
		}
		// 조회수 검사 (한글 또는 영어 포함)
		if strings.Contains(trimmed, "조회수") || strings.Contains(trimmed, "Views") {
			viewCountColIdx = i
			log.Printf("   ✅ '조회수/Views' 포함 컬럼 발견: '%s' (위치: %d)", trimmed, i+1)
		}
		// 시청 시간 검사 (한글 또는 영어 포함)
		if strings.Contains(trimmed, "시청 시간") || strings.Contains(trimmed, "Watch") {
			watchTimeColIdx = i
			log.Printf("   ✅ '시청 시간/Watch' 포함 컬럼 발견: '%s' (위치: %d)", trimmed, i+1)
		}
	}

	if !hasAge || !hasGender {
		missingCols := []string{}
		if !hasAge {
			missingCols = append(missingCols, "시청자 연령/Viewer age")
		}
		if !hasGender {
			missingCols = append(missingCols, "시청자 성별/Viewer gender")
		}
		result.Message = fmt.Sprintf("필수 컬럼이 없습니다: %v", missingCols)
		result.MissingCols = missingCols
		return result
	}

	if viewCountColIdx == -1 {
		result.Message = "'조회수/Views'가 포함된 컬럼이 없습니다"
		return result
	}

	if watchTimeColIdx == -1 {
		result.Message = "'시청 시간/Watch'가 포함된 컬럼이 없습니다"
		return result
	}

	// // ✅ 검증 4: 조회수 합계 = 100 (주석처리)
	// log.Printf("🔍 검증 4: 조회수 합계 확인 (100)")

	// viewCountSum := 0.0
	// for _, row := range dataRows {
	// 	if viewCountColIdx < len(row) {
	// 		val := strings.TrimSpace(row[viewCountColIdx])
	// 		val = strings.ReplaceAll(val, "%", "")
	// 		if num, err := strconv.ParseFloat(val, 64); err == nil {
	// 			viewCountSum += num
	// 		}
	// 	}
	// }

	// log.Printf("   조회수 합계: %.2f", viewCountSum)
	// result.Details["viewCountSum"] = viewCountSum

	// if viewCountSum < 99.0 || viewCountSum > 100.01 {
	// 	log.Printf("   ❌ 조회수 합계가 99~100 범위가 아닙니다 (현재: %.2f)", viewCountSum)
	// 	result.Message = fmt.Sprintf("조회수 합계가 99~100 범위가 아닙니다 (현재: %.2f)", viewCountSum)
	// 	return result
	// }
	// log.Printf("   ✅ 조회수 합계 99~100 확인 완료")

	// // ✅ 검증 5: 시청 시간 합계 = 100 (주석처리)
	// log.Printf("🔍 검증 5: 시청 시간 합계 확인 (100)")

	// watchTimeSum := 0.0
	// for _, row := range dataRows {
	// 	if watchTimeColIdx < len(row) {
	// 		val := strings.TrimSpace(row[watchTimeColIdx])
	// 		val = strings.ReplaceAll(val, "%", "")
	// 		if num, err := strconv.ParseFloat(val, 64); err == nil {
	// 			watchTimeSum += num
	// 		}
	// 	}
	// }

	// log.Printf("   시청 시간 합계: %.2f", watchTimeSum)
	// result.Details["watchTimeSum"] = watchTimeSum

	// if watchTimeSum < 99.0 || watchTimeSum > 100.01 {
	// 	log.Printf("   ❌ 시청 시간 합계가 99~100 범위가 아닙니다 (현재: %.2f)", watchTimeSum)
	// 	result.Message = fmt.Sprintf("시청 시간 합계가 99~100 범위가 아닙니다 (현재: %.2f)", watchTimeSum)
	// 	return result
	// }
	// log.Printf("   ✅ 시청 시간 합계 99~100 확인 완료")

	// ✅ 모든 검증 통과
	result.Success = true
	result.TableDataOK = true
	result.Message = "검증 성공: 표 데이터.csv 모든 검증 통과"

	return result
}

func extractFileName(path string) string {
	fileName := path
	if idx := strings.LastIndex(fileName, "/"); idx != -1 {
		fileName = fileName[idx+1:]
	}
	if idx := strings.LastIndex(fileName, "\\"); idx != -1 {
		fileName = fileName[idx+1:]
	}
	return fileName
}

// parseGeographyDataFromBytes - 지역 데이터 파싱 (메모리에서)
// 반환값: (데이터 맵, 데이터 없음 여부, 에러)
func parseGeographyDataFromBytes(fileBytes []byte) (map[string]interface{}, bool, error) {
	reader, err := zip.NewReader(bytes.NewReader(fileBytes), int64(len(fileBytes)))
	if err != nil {
		return nil, false, fmt.Errorf("ZIP 파일 열기 실패: %v", err)
	}

	// "표 데이터" 또는 "Chart" 파일 찾기
	var tableDataFile *zip.File
	for _, f := range reader.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
			fileName := extractFileName(f.Name)
			if strings.Contains(fileName, "표 데이터") || strings.Contains(fileName, "Chart") {
				tableDataFile = f
				break
			}
		}
	}

	if tableDataFile == nil {
		return nil, false, fmt.Errorf("표 데이터 또는 Chart 파일을 찾을 수 없습니다")
	}

	// CSV 파일 읽기
	rc, err := tableDataFile.Open()
	if err != nil {
		return nil, false, fmt.Errorf("CSV 파일 열기 실패: %v", err)
	}
	defer rc.Close()

	csvReader := csv.NewReader(rc)
	records, err := csvReader.ReadAll()
	if err != nil {
		return nil, false, fmt.Errorf("CSV 읽기 실패: %v", err)
	}

	if len(records) < 2 {
		log.Printf("⚠️ CSV 파일에 데이터가 없습니다 (헤더만 존재)")
		return map[string]interface{}{
			"isEmpty": true,
		}, true, nil
	}

	headers := records[0]
	dataRows := records[1:]

	// "지역/Geography", "조회수/Views" 컬럼 찾기
	regionColIdx := -1
	viewsColIdx := -1
	for i, header := range headers {
		trimmed := strings.TrimSpace(header)
		if trimmed == "지역" || trimmed == "Geography" {
			regionColIdx = i
		}
		if trimmed == "조회수" || trimmed == "Views" {
			viewsColIdx = i
		}
	}

	if regionColIdx == -1 || viewsColIdx == -1 {
		return nil, false, fmt.Errorf("지역/Geography 또는 조회수/Views 컬럼을 찾을 수 없습니다")
	}

	// 데이터 파싱
	rawData := make(map[string]int)
	totalViews := 0

	for _, row := range dataRows {
		if len(row) <= regionColIdx || len(row) <= viewsColIdx {
			continue
		}

		region := strings.TrimSpace(row[regionColIdx])
		viewsStr := strings.TrimSpace(row[viewsColIdx])

		// "합계" 행은 건너뛰기
		if region == "합계" || region == "" {
			continue
		}

		// 조회수 파싱 (쉼표 제거)
		viewsStr = strings.ReplaceAll(viewsStr, ",", "")
		views, err := strconv.Atoi(viewsStr)
		if err != nil {
			log.Printf("⚠️ 조회수 파싱 실패 (%s: %s), 건너뛰기", region, viewsStr)
			continue
		}

		rawData[region] = views
		totalViews += views
	}

	if totalViews == 0 {
		return nil, false, fmt.Errorf("유효한 조회수 데이터가 없습니다")
	}

	// topCountries 생성
	type countryData struct {
		Country    string  `json:"country"`
		Views      int     `json:"views"`
		Percentage float64 `json:"percentage"`
	}

	topCountries := []countryData{}
	for country, views := range rawData {
		percentage := (float64(views) / float64(totalViews)) * 100
		topCountries = append(topCountries, countryData{
			Country:    country,
			Views:      views,
			Percentage: math.Round(percentage*10) / 10,
		})
	}

	// 조회수 내림차순 정렬
	sort.Slice(topCountries, func(i, j int) bool {
		return topCountries[i].Views > topCountries[j].Views
	})

	log.Printf("✅ 지역 데이터 파싱 완료: 총 %d개국, 총 조회수 %s", len(rawData), formatNumber(totalViews))

	return map[string]interface{}{
		"isEmpty":        false,
		"rawData":        rawData,
		"totalViews":     totalViews,
		"topCountries":   topCountries,
		"totalCountries": len(rawData),
		"uploadedAt":     time.Now(),
	}, false, nil
}

// parseDemographicsDataFromBytes - 연령&성별 데이터 파싱 (메모리에서)
// 반환값: (데이터 맵, 데이터 없음 여부, 에러)
func parseDemographicsDataFromBytes(fileBytes []byte, baseViews int) (map[string]interface{}, bool, error) {
	reader, err := zip.NewReader(bytes.NewReader(fileBytes), int64(len(fileBytes)))
	if err != nil {
		return nil, false, fmt.Errorf("ZIP 파일 열기 실패: %v", err)
	}

	// "표 데이터" 또는 "Chart" 파일 찾기
	var tableDataFile *zip.File
	for _, f := range reader.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
			fileName := extractFileName(f.Name)
			if strings.Contains(fileName, "표 데이터") || strings.Contains(fileName, "Chart") {
				tableDataFile = f
				break
			}
		}
	}

	if tableDataFile == nil {
		return nil, false, fmt.Errorf("표 데이터 또는 Chart 파일을 찾을 수 없습니다")
	}

	// CSV 파일 읽기
	rc, err := tableDataFile.Open()
	if err != nil {
		return nil, false, fmt.Errorf("CSV 파일 열기 실패: %v", err)
	}
	defer rc.Close()

	csvReader := csv.NewReader(rc)
	records, err := csvReader.ReadAll()
	if err != nil {
		return nil, false, fmt.Errorf("CSV 읽기 실패: %v", err)
	}

	if len(records) < 2 {
		log.Printf("⚠️ CSV 파일에 데이터가 없습니다 (헤더만 존재)")
		return map[string]interface{}{
			"isEmpty": true,
		}, true, nil
	}

	headers := records[0]
	dataRows := records[1:]

	// "시청자 연령/Viewer age", "시청자 성별/Viewer gender", "조회수/Views" 컬럼 찾기
	ageColIdx := -1
	genderColIdx := -1
	viewPctColIdx := -1

	for i, header := range headers {
		trimmed := strings.TrimSpace(header)
		if trimmed == "시청자 연령" || trimmed == "Viewer age" {
			ageColIdx = i
		}
		if trimmed == "시청자 성별" || trimmed == "Viewer gender" {
			genderColIdx = i
		}
		if strings.Contains(trimmed, "조회수") || strings.Contains(trimmed, "Views") {
			viewPctColIdx = i
		}
	}

	if ageColIdx == -1 || genderColIdx == -1 || viewPctColIdx == -1 {
		return nil, false, fmt.Errorf("필수 컬럼을 찾을 수 없습니다 (시청자 연령/Viewer age, 시청자 성별/Viewer gender, 조회수/Views)")
	}

	// 데이터 파싱
	rawData := []map[string]interface{}{}
	genderSum := make(map[string]float64) // male, female
	ageSum := make(map[string]float64)    // age13-17, age18-24, ...

	for _, row := range dataRows {
		if len(row) <= ageColIdx || len(row) <= genderColIdx || len(row) <= viewPctColIdx {
			continue
		}

		ageRange := strings.TrimSpace(row[ageColIdx])       // "13-17세"
		gender := strings.TrimSpace(row[genderColIdx])      // "남성" or "여성"
		viewPctStr := strings.TrimSpace(row[viewPctColIdx]) // "15.5%"

		if ageRange == "" || gender == "" || viewPctStr == "" {
			continue
		}

		// % 제거 후 숫자 변환
		viewPctStr = strings.ReplaceAll(viewPctStr, "%", "")
		viewPct, err := strconv.ParseFloat(viewPctStr, 64)
		if err != nil {
			log.Printf("⚠️ 조회수 퍼센트 파싱 실패 (%s/%s: %s), 건너뛰기", ageRange, gender, viewPctStr)
			continue
		}

		// 실제 조회수 계산
		calculatedViews := int(math.Round(float64(baseViews) * viewPct / 100))

		// rawData 저장
		rawData = append(rawData, map[string]interface{}{
			"age":             ageRange,
			"gender":          gender,
			"viewPercentage":  viewPct,
			"calculatedViews": calculatedViews,
		})

		// 성별 집계
		genderKey := convertGenderToEng(gender)
		genderSum[genderKey] += viewPct

		// 연령대 집계
		ageKey := convertAgeRangeToKey(ageRange)
		ageSum[ageKey] += viewPct

		// 디버그 로그 (첫 10개 행만)
		if len(rawData) < 10 {
			log.Printf("   파싱: '%s' + '%s' → gender='%s', age='%s', %.2f%%", ageRange, gender, genderKey, ageKey, viewPct)
		}
	}

	log.Printf("✅ 연령&성별 데이터 파싱 완료: %d개 행, 기준 조회수 %s", len(rawData), formatNumber(baseViews))

	return map[string]interface{}{
		"isEmpty":    false,
		"rawData":    rawData,
		"gender":     genderSum,
		"ageGroup":   ageSum,
		"baseViews":  baseViews,
		"uploadedAt": time.Now(),
	}, false, nil
}

// convertGenderToEng - 성별 한글/영문 → 표준 영문 변환 (유연한 처리)
// 예: "남성", "male", "Male", "M" → "male"
//     "여성", "female", "Female", "F" → "female"
func convertGenderToEng(gender string) string {
	// 소문자 변환 및 trim
	genderLower := strings.ToLower(strings.TrimSpace(gender))

	// 남성 판별
	if strings.Contains(genderLower, "남") || // 한글 "남성"
		genderLower == "male" || // 영어 "male"
		genderLower == "m" || // 약어 "M"
		strings.Contains(genderLower, "man") { // "man", "men"
		return "male"
	}

	// 여성 판별
	if strings.Contains(genderLower, "여") || // 한글 "여성"
		genderLower == "female" || // 영어 "female"
		genderLower == "f" || // 약어 "F"
		strings.Contains(genderLower, "woman") { // "woman", "women"
		return "female"
	}

	// 기본값 (파싱 실패 시 female로)
	log.Printf("⚠️ 알 수 없는 성별 형식: '%s', 'female'로 처리", gender)
	return "female"
}

// convertAgeRangeToKey - 연령대 키 변환 (유연한 범주화)
// 예: "13-17세", "13–17 years", "13~17" → "age13-17"
// 첫 번째 숫자를 기준으로 표준 범주에 매핑
func convertAgeRangeToKey(ageRange string) string {
	// 연령대 범주 매핑
	ageMap := map[int]string{
		13: "age13-17",
		18: "age18-24",
		25: "age25-34",
		35: "age35-44",
		45: "age45-54",
		55: "age55-64",
		65: "age65+",
	}

	// 첫 번째 숫자 추출 (정규식 또는 파싱)
	var firstNum int
	for i := 0; i < len(ageRange); i++ {
		if ageRange[i] >= '0' && ageRange[i] <= '9' {
			// 연속된 숫자 추출
			numStr := ""
			for j := i; j < len(ageRange) && ageRange[j] >= '0' && ageRange[j] <= '9'; j++ {
				numStr += string(ageRange[j])
			}
			if num, err := strconv.Atoi(numStr); err == nil {
				firstNum = num
				break
			}
		}
	}

	// 범주 매핑 (가장 가까운 범주 찾기)
	if key, exists := ageMap[firstNum]; exists {
		return key
	}

	// 매핑되지 않은 경우 가장 가까운 범주 찾기
	if firstNum < 13 {
		return "age13-17" // 13세 미만은 13-17로
	} else if firstNum >= 13 && firstNum < 18 {
		return "age13-17"
	} else if firstNum >= 18 && firstNum < 25 {
		return "age18-24"
	} else if firstNum >= 25 && firstNum < 35 {
		return "age25-34"
	} else if firstNum >= 35 && firstNum < 45 {
		return "age35-44"
	} else if firstNum >= 45 && firstNum < 55 {
		return "age45-54"
	} else if firstNum >= 55 && firstNum < 65 {
		return "age55-64"
	} else {
		return "age65+" // 65세 이상
	}
}

// aggregateCompetitionStats - 대회 전체 통계 집계
func (h *CSVHandler) aggregateCompetitionStats(ctx context.Context, competitionID string) error {
	log.Printf("📊 대회 통계 집계 시작: %s", competitionID)

	// analytics/videos/list 모든 문서 조회
	videoDocs, err := h.firestore.Collection("competitions").Doc(competitionID).
		Collection("analytics").Doc("videos").Collection("list").Documents(ctx).GetAll()

	if err != nil {
		return fmt.Errorf("비디오 통계 조회 실패: %v", err)
	}

	if len(videoDocs) == 0 {
		log.Printf("⚠️ 집계할 비디오 통계가 없습니다")
		return nil
	}

	// 집계 변수
	totalGender := make(map[string]float64)
	totalAge := make(map[string]float64)
	allCountries := make(map[string]int)
	videoCount := 0

	for _, doc := range videoDocs {
		data := doc.Data()

		// Demographics 집계
		if demo, ok := data["demographics"].(map[string]interface{}); ok {
			if gender, ok := demo["gender"].(map[string]interface{}); ok {
				for k, v := range gender {
					if val, ok := v.(float64); ok {
						totalGender[k] += val
					}
				}
			}
			if age, ok := demo["ageGroup"].(map[string]interface{}); ok {
				for k, v := range age {
					if val, ok := v.(float64); ok {
						totalAge[k] += val
					}
				}
			}
		}

		// Geography 집계
		if geo, ok := data["geography"].(map[string]interface{}); ok {
			if rawData, ok := geo["rawData"].(map[string]interface{}); ok {
				for country, views := range rawData {
					if val, ok := views.(int64); ok {
						allCountries[country] += int(val)
					}
				}
			}
		}

		videoCount++
	}

	// 평균 계산 (성별, 연령대)
	for k := range totalGender {
		totalGender[k] /= float64(videoCount)
		totalGender[k] = math.Round(totalGender[k]*10) / 10
	}
	for k := range totalAge {
		totalAge[k] /= float64(videoCount)
		totalAge[k] = math.Round(totalAge[k]*10) / 10
	}

	// topCountries 생성
	type countryData struct {
		Country    string  `json:"country"`
		Views      int     `json:"views"`
		Percentage float64 `json:"percentage"`
	}

	topCountries := []countryData{}
	totalCountryViews := 0
	for _, views := range allCountries {
		totalCountryViews += views
	}

	for country, views := range allCountries {
		percentage := (float64(views) / float64(totalCountryViews)) * 100
		topCountries = append(topCountries, countryData{
			Country:    country,
			Views:      views,
			Percentage: math.Round(percentage*10) / 10,
		})
	}

	// 조회수 내림차순 정렬
	sort.Slice(topCountries, func(i, j int) bool {
		return topCountries[i].Views > topCountries[j].Views
	})

	// analytics/report 업데이트
	_, err = h.firestore.Collection("competitions").Doc(competitionID).
		Collection("analytics").Doc("report").Set(ctx, map[string]interface{}{
		"detailedStats": map[string]interface{}{
			"demographics": map[string]interface{}{
				"gender":   totalGender,
				"ageGroup": totalAge,
			},
			"geography": map[string]interface{}{
				"topCountries":   topCountries,
				"totalCountries": len(allCountries),
			},
		},
		"metadata": map[string]interface{}{
			"hasDetailedStats": videoCount > 0,
			"videoCount":       videoCount,
			"lastUpdated":      time.Now(),
		},
	}, firestore.MergeAll)

	if err != nil {
		return fmt.Errorf("report 업데이트 실패: %v", err)
	}

	log.Printf("✅ 대회 통계 집계 완료: %d개 비디오, %d개국", videoCount, len(allCountries))
	return nil
}

// formatNumber - 숫자 포매팅 (쉼표 추가)
func formatNumber(num int) string {
	str := strconv.Itoa(num)
	n := len(str)
	if n <= 3 {
		return str
	}

	result := ""
	for i, c := range str {
		if i > 0 && (n-i)%3 == 0 {
			result += ","
		}
		result += string(c)
	}
	return result
}
