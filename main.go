package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var (
	clients = make(map[*websocket.Conn]bool)
)

var (
	game     *Game
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

type Word struct {
	WordID      int    `json:"word_id"`
	WordEntry   string `json:"word_entry"`
	Description string `json:"description"`
}

// WordList represents a list of words
type WordList struct {
	Words []Word `json:"words"`
}

type GameState struct {
	Turn         int                     `json:"turn"`
	Word         string                  `json:"word"`
	RevealedWord string                  `json:"revealedWord"`
	Description  string                  `json:"description"`
	Scores       map[int]int             `json:"scores"`
	Players      map[int]string          `json:"players"`
	ActivePlayer int                     `json:"activePlayer"`
	WordList     []Word                  `json:"wordList"`
	Connected    map[int]*websocket.Conn `json:"-"`
	Message      string                  `json:"message"`
}

func main() {
	// Wonder Words Game
	game = NewGame()

	router := gin.Default()

	router.LoadHTMLGlob("*.html")

	router.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})

	router.GET("/ws", handleWebSocket)

	err := router.Run(":8080")
	if err != nil {
		log.Fatal(err)
	}

}

// Wonder Words Game

func handleWebSocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println(err)
		return
	}

	playerID := game.AddPlayer(conn)
	defer game.RemovePlayer(playerID)

	err = conn.WriteJSON(game.GetGameState(playerID))
	if err != nil {
		log.Println(err)
		return
	}

	go handlePlayerInput(conn, playerID, game, c)
}

func handlePlayerInput(conn *websocket.Conn, playerID int, game *Game, c *gin.Context) {
	for {
		var guess GuessRequest
		err := conn.ReadJSON(&guess)
		if err != nil {
			log.Println(err)
			break
		}

		game.MakeGuess(playerID, guess.Character)
		game.NextTurn()

		gameState := game.GetGameState(playerID)
		gameState.Scores = game.Scores
		err = game.BroadcastGameState(strconv.Itoa(playerID), c)
		if err != nil {
			log.Println(err)
			break
		}
	}
	conn.Close()
}

type GuessRequest struct {
	Character string `json:"character"`
	PlayerID  string `json:"playerID"`
}

type Game struct {
	Players      map[int]*websocket.Conn
	Turn         int
	Word         string
	RevealedWord string
	Description  string
	Scores       map[int]int
	WordList     []Word
	Message      string
	server       *http.Server
}

func NewGame() *Game {
	wordList, err := loadWordList("wordlist.json")
	if err != nil {
		log.Fatal(err)
	}

	rand.Seed(time.Now().UnixNano())
	wordIndex := rand.Intn(len(wordList))
	word := wordList[wordIndex]
	// log.Println(word.WordEntry)

	// Create a revealed word with spaces instead of underscores
	revealedWord := ""
	for _, char := range word.WordEntry {
		if char == ' ' {
			revealedWord += " "
		} else {
			revealedWord += "_"
		}
	}

	return &Game{
		Players:      make(map[int]*websocket.Conn),
		Turn:         1,
		Word:         word.WordEntry,
		RevealedWord: revealedWord,
		Description:  word.Description,
		Scores:       make(map[int]int),
	}
}

func (g *Game) AddPlayer(conn *websocket.Conn) int {
	playerID := len(g.Players) + 1
	g.Players[playerID] = conn
	g.Scores[playerID] = 0
	g.BroadcastPlayerJoined(playerID)

	return playerID
}

func (g *Game) RemovePlayer(playerID int) {
	delete(g.Players, playerID)
	delete(g.Scores, playerID)
	g.BroadcastPlayerLeft(playerID)
}

var totalScore int
var moves = 3

func (g *Game) MakeGuess(playerID int, character string) bool {
	var isCorrect = false
	if g.Turn == playerID {
		word := []rune(g.Word)
		revealedWord := []rune(g.RevealedWord)
		count := 0

		// Check if the character is already revealed
		if !strings.Contains(g.RevealedWord, strings.ToLower(character)) && !strings.Contains(g.RevealedWord, strings.ToUpper(character)) {
			for i, c := range word {
				if string(c) == strings.ToLower(character) || string(c) == strings.ToUpper(character) {
					revealedWord[i] = c
					isCorrect = true
					count++
				}
			}
		}

		g.RevealedWord = string(revealedWord)

		if !strings.Contains(g.RevealedWord, "-") {
			score := 100 * count

			if isCorrect {
				totalScore += score
				g.Message = fmt.Sprintf("Right guess! You have a total of %d points.", totalScore)
				g.NextTurn()
				if g.RevealedWord == g.Word {
					g.EndGame()
				}
			} else {
				if moves == 1 {
					g.EndGame()
				}
				totalScore -= 100
				g.Message = fmt.Sprintf("Wrong guess! You have a total of %d points. You have %d moves remaining.", totalScore, moves-1)
				g.NextTurn()
				moves--
			}
		}
	}

	return isCorrect
}

func (g *Game) NextTurn() {
	g.Turn++
	if g.Turn > len(g.Players) {
		g.Turn = 1
	}
}

func (g *Game) GetGameState(playerID int) GameState {

	return GameState{
		Turn:         g.Turn,
		Word:         g.Word,
		RevealedWord: g.RevealedWord,
		Description:  g.Description,
		Scores:       g.Scores,
		Players:      getPlayerNames(g.Players),
		ActivePlayer: g.Turn,
		WordList:     g.WordList,
		Connected:    g.Players,
		Message:      g.Message,
	}
}

func (g *Game) BroadcastGameState(playerID string, c *gin.Context) error {
	convertedPlayerID, err := strconv.Atoi(playerID)
	if err != nil {
		return err
	}
	gameState := g.GetGameState(convertedPlayerID)
	gameState.Scores = g.Scores

	jsonBytes, err := json.Marshal(gameState)
	if err != nil {
		return err
	}

	for id, conn := range g.Players {
		if id != gameState.ActivePlayer {
			err := conn.WriteMessage(websocket.TextMessage, jsonBytes)
			if err != nil {
				log.Println(err)
				return err
			}
		}
	}

	return nil
}

func (g *Game) BroadcastPlayerJoined(playerID int) {
	message := struct {
		Event     string `json:"event"`
		PlayerID  int    `json:"playerID"`
		PlayerIDs []int  `json:"playerIDs"`
	}{
		Event:     "playerJoined",
		PlayerID:  playerID,
		PlayerIDs: getPlayerIDs(g.Players),
	}

	jsonBytes, err := json.Marshal(message)
	if err != nil {
		log.Println(err)
		return
	}

	for _, conn := range g.Players {
		err := conn.WriteMessage(websocket.TextMessage, jsonBytes)
		if err != nil {
			log.Println(err)
			return
		}
	}
}

func (g *Game) BroadcastPlayerLeft(playerID int) {
	message := struct {
		Event     string `json:"event"`
		PlayerID  int    `json:"playerID"`
		PlayerIDs []int  `json:"playerIDs"`
	}{
		Event:     "playerLeft",
		PlayerID:  playerID,
		PlayerIDs: getPlayerIDs(g.Players),
	}

	jsonBytes, err := json.Marshal(message)
	if err != nil {
		log.Println(err)
		return
	}

	for _, conn := range g.Players {
		err := conn.WriteMessage(websocket.TextMessage, jsonBytes)
		if err != nil {
			log.Println(err)
			return
		}
	}
}

func getPlayerNames(players map[int]*websocket.Conn) map[int]string {
	playerNames := make(map[int]string)
	for id := range players {
		playerNames[id] = getPlayerName(id)
	}
	return playerNames
}

func getPlayerIDs(players map[int]*websocket.Conn) []int {
	playerIDs := make([]int, 0)
	for id := range players {
		playerIDs = append(playerIDs, id)
	}
	return playerIDs
}

func getPlayerName(playerID int) string {
	return "Player " + strconv.Itoa(playerID)
}

func loadWordList(filename string) ([]Word, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var wordList []Word
	err = json.NewDecoder(file).Decode(&wordList)
	if err != nil {
		return nil, err
	}

	return wordList, nil
}

func countCharacter(word string, character string) int {
	count := 0
	for _, c := range word {
		if string(c) == character {
			count++
		}
	}
	return count
}

func (g *Game) EndGame() {
	// Reset the game state or perform any necessary actions
	g.Turn = 1
	g.Word = ""
	// g.RevealedWord = ""
	g.Description = ""
	g.Scores = make(map[int]int)
	g.Players = make(map[int]*websocket.Conn)
	g.WordList = nil

	// Send game over message to the clients
	message := struct {
		Event string `json:"event"`
	}{
		Event: "gameOver",
	}

	jsonBytes, err := json.Marshal(message)
	if err != nil {
		log.Println(err)
		return
	}

	for _, conn := range g.Players {
		err := conn.WriteMessage(websocket.TextMessage, jsonBytes)
		if err != nil {
			log.Println(err)
			return
		}
	}

}
