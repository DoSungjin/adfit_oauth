package services

import (
	"context"
	"fmt"
	"log"
	"sync"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/db"
	"google.golang.org/api/option"

	"adfit-oauth/config"
)

// FirestoreClients manages multiple Firestore database clients
type FirestoreClients struct {
	DefaultDB  *firestore.Client
	TestDB     *firestore.Client
	RealtimeDB *db.Client
	app        *firebase.App
}

var (
	firestoreClients *FirestoreClients
	clientsOnce      sync.Once
	clientsErr       error
)

// InitFirestoreClients initializes both default and test Firestore clients
func InitFirestoreClients() (*FirestoreClients, error) {
	clientsOnce.Do(func() {
		ctx := context.Background()

		// Firebase App 초기화
		var app *firebase.App
		var err error

		databaseURL := "https://posted-app-c4ff5-default-rtdb.firebaseio.com"
		if config.Config != nil && config.Config.Firebase.DatabaseURL != "" {
			databaseURL = config.Config.Firebase.DatabaseURL
		}

		projectID := "posted-app-c4ff5"
		if config.Config != nil && config.Config.Firebase.ProjectID != "" {
			projectID = config.Config.Firebase.ProjectID
		}

		credentialsPath := ""
		if config.Config != nil {
			credentialsPath = config.Config.Firebase.CredentialsPath
		}

		if credentialsPath != "" {
			app, err = firebase.NewApp(ctx, &firebase.Config{
				ProjectID:   projectID,
				DatabaseURL: databaseURL,
			}, option.WithCredentialsFile(credentialsPath))
		} else {
			app, err = firebase.NewApp(ctx, &firebase.Config{
				ProjectID:   projectID,
				DatabaseURL: databaseURL,
			})
		}

		if err != nil {
			clientsErr = fmt.Errorf("firebase 초기화 실패: %v", err)
			return
		}

		// Default Firestore 클라이언트 (Firebase Admin SDK 사용)
		defaultClient, err := app.Firestore(ctx)
		if err != nil {
			clientsErr = fmt.Errorf("default firestore 초기화 실패: %v", err)
			return
		}
		log.Println("✅ Firestore (default) 연결 완료")

		// Test Firestore 클라이언트 (adtown-test) - cloud.google.com/go/firestore 사용
		var testClient *firestore.Client
		testDBID := config.GetTestDatabaseID()
		
		if credentialsPath != "" {
			testClient, err = firestore.NewClientWithDatabase(ctx, projectID, testDBID, option.WithCredentialsFile(credentialsPath))
		} else {
			testClient, err = firestore.NewClientWithDatabase(ctx, projectID, testDBID)
		}
		
		if err != nil {
			log.Printf("⚠️ Firestore (%s) 연결 실패: %v (계속 진행)", testDBID, err)
			testClient = nil
		} else {
			log.Printf("✅ Firestore (%s) 연결 완료", testDBID)
		}

		// Realtime Database 클라이언트
		realtimeClient, err := app.Database(ctx)
		if err != nil {
			log.Printf("⚠️ Realtime Database 연결 실패: %v (계속 진행)", err)
			realtimeClient = nil
		} else {
			log.Println("✅ Realtime Database 연결 완료")
		}

		firestoreClients = &FirestoreClients{
			DefaultDB:  defaultClient,
			TestDB:     testClient,
			RealtimeDB: realtimeClient,
			app:        app,
		}
	})

	return firestoreClients, clientsErr
}

// GetFirestoreClients returns the singleton FirestoreClients instance
func GetFirestoreClients() *FirestoreClients {
	if firestoreClients == nil {
		clients, err := InitFirestoreClients()
		if err != nil {
			log.Printf("❌ FirestoreClients 초기화 실패: %v", err)
			return nil
		}
		return clients
	}
	return firestoreClients
}

// GetDefaultDB returns the default Firestore client
func (fc *FirestoreClients) GetDefaultDB() *firestore.Client {
	return fc.DefaultDB
}

// GetTestDB returns the test Firestore client (adtown-test)
func (fc *FirestoreClients) GetTestDB() *firestore.Client {
	return fc.TestDB
}

// GetRealtimeDB returns the Realtime Database client
func (fc *FirestoreClients) GetRealtimeDB() *db.Client {
	return fc.RealtimeDB
}

// FindCompetitionDB finds which database contains the competition
// Returns the appropriate Firestore client and a boolean indicating if it's test DB
func (fc *FirestoreClients) FindCompetitionDB(ctx context.Context, competitionID string) (*firestore.Client, bool, error) {
	// 1. Default DB에서 먼저 찾기
	if fc.DefaultDB != nil {
		doc, err := fc.DefaultDB.Collection("competitions").Doc(competitionID).Get(ctx)
		if err == nil && doc.Exists() {
			return fc.DefaultDB, false, nil
		}
	}

	// 2. Test DB에서 찾기
	if fc.TestDB != nil {
		doc, err := fc.TestDB.Collection("competitions").Doc(competitionID).Get(ctx)
		if err == nil && doc.Exists() {
			return fc.TestDB, true, nil
		}
	}

	return nil, false, fmt.Errorf("competition not found in any database: %s", competitionID)
}

// GetAllActiveCompetitions returns all active competitions from both databases
func (fc *FirestoreClients) GetAllActiveCompetitions(ctx context.Context, statuses []string) ([]CompetitionRef, error) {
	var results []CompetitionRef

	// Default DB에서 조회
	if fc.DefaultDB != nil {
		competitions, err := fc.getActiveCompetitionsFromDB(ctx, fc.DefaultDB, statuses, false)
		if err != nil {
			log.Printf("⚠️ Default DB 대회 조회 실패: %v", err)
		} else {
			results = append(results, competitions...)
		}
	}

	// Test DB에서 조회
	if fc.TestDB != nil {
		competitions, err := fc.getActiveCompetitionsFromDB(ctx, fc.TestDB, statuses, true)
		if err != nil {
			log.Printf("⚠️ Test DB 대회 조회 실패: %v", err)
		} else {
			results = append(results, competitions...)
		}
	}

	return results, nil
}

// CompetitionRef holds a reference to a competition with its database info
type CompetitionRef struct {
	ID       string
	DB       *firestore.Client
	IsTestDB bool
	Data     map[string]interface{}
}

// getActiveCompetitionsFromDB fetches active competitions from a specific database
func (fc *FirestoreClients) getActiveCompetitionsFromDB(ctx context.Context, db *firestore.Client, statuses []string, isTestDB bool) ([]CompetitionRef, error) {
	var results []CompetitionRef

	for _, status := range statuses {
		iter := db.Collection("competitions").
			Where("status", "==", status).
			Where("deleted", "==", false).
			Documents(ctx)

		for {
			doc, err := iter.Next()
			if err != nil {
				break // iterator.Done or error
			}

			results = append(results, CompetitionRef{
				ID:       doc.Ref.ID,
				DB:       db,
				IsTestDB: isTestDB,
				Data:     doc.Data(),
			})
		}
	}

	return results, nil
}
