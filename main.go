package main

import (
	"encoding/json"
	"flag"
	"image"
	"image/draw"
	"image/jpeg"
	"log"
	"math/rand"
	"net/http"
	"net/http/fcgi"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/rwtodd/apputil-go/resource"
)

var local = flag.String("local", "", "serve as webserver on this localhost port (e.g., 8000)")

// hold a global cached deck between requests...
type cacheEnt struct {
	rqno int
	dck  *deck
}

var requestCount int
var deckCache map[string]cacheEnt
var deckMut sync.Mutex

func requestDeck(name string) (*deck, error) {
	var answer *deck
	var err error

	fullname := filepath.Join(rscBase, name)

	deckMut.Lock()

	// update a request count, which wraps at 10k
	requestCount++
	if requestCount > 9999 {
		requestCount = 1
	}

	if deckCache == nil {
		deckCache = make(map[string]cacheEnt)
	}

	ent, ok := deckCache[name]
	if !ok {
		answer, err = NewDeck(fullname)
		if err == nil {
			ent = cacheEnt{requestCount, answer}
			deckCache[name] = ent
		}
	}

	// update the request number...
	if err == nil {
		ent.rqno = requestCount
		deckCache[name] = ent

		// record the deck to return:
		answer = ent.dck
	}

	// clean out any old decks...
	for key, value := range deckCache {

		var difference int // abs difference, rqno and requestCount
		if value.rqno <= requestCount {
			difference = requestCount - value.rqno
		} else {
			difference = (9999 + requestCount) - value.rqno
		}

		if difference > 10 {
			value.dck.Close()
			delete(deckCache, key)
		}

	}
	deckMut.Unlock()

	// try to fall back on the poker deck... it's safe!
	if err != nil && name != "Poker.zip" {
		answer, err = requestDeck("Poker.zip")
	}

	return answer, err
}

// rscBase is the base path of our resources
var rscBase string

func main() {
	var err error
	flag.Parse()

	loc := resource.NewPathLocator([]string{"."}, true)
	rscBase, err = loc.Path("github.com/rwtodd/carddiv-go")
	if err != nil {
		log.Fatal(err)
	}

	rand.Seed(time.Now().UnixNano())

	http.HandleFunc("/", mainHandler)
	http.HandleFunc("/carddiv/cdiv.css", cssHandler)
	http.HandleFunc("/carddiv/cfg", cfgHandler)

	http.HandleFunc("/carddiv/row/", rowHandler)
	http.HandleFunc("/carddiv/houses/", houseHandler)
	http.HandleFunc("/carddiv/celtic/", celticHandler)
	http.HandleFunc("/carddiv/tableau/", tableauHandler)

	if *local != "" {
		err = http.ListenAndServe("localhost:"+*local, nil)
	} else {
		err = fcgi.Serve(nil, nil)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func mainHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join(rscBase, "index.html"))
}

func cssHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join(rscBase, "cdiv.css"))
}

func cfgHandler(w http.ResponseWriter, r *http.Request) {
	cfg, err := json.Marshal(configurations)
	if err != nil {
		log.Fatal(err)
	}
	w.Write(cfg)
}

func getOrElse(lst []string, def string) string {
	if len(lst) > 0 {
		def = lst[0]
	}
	return def
}

// tableauHandler generates an image of cards in a "Grand Tableau",
// of 4 rows of 8 and 1 row of 4
func tableauHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Print(err)
	}

	desiredWidth, _ := strconv.Atoi(getOrElse(r.Form["width"], "600"))
	desiredReversals, _ := strconv.Atoi(getOrElse(r.Form["rev"], "50"))
	desiredDeck := getOrElse(r.Form["deck"], "Poker")
	log.Printf("TABLEAU: %s Width: %d Reversals: %d%%",
		desiredDeck,
		desiredWidth,
		desiredReversals)
	revN := 1.0 - float64(desiredReversals)/100.0

	deck, err := requestDeck(desiredDeck + ".zip")
	if err != nil {
		log.Print(err)
		return
	}

	cardWidth := int(float64(desiredWidth) / 8.0)
	cardHeight := deck.CardHeight(cardWidth)

	// now, shuffle the deck
	selected, err := deck.Shuffled(36)
	if err != nil {
		log.Print(err)
		return
	}

	// now, create the image
	actualWidth := int(8.0 * float64(cardWidth))
	actualHeight := int(5.0 * float64(cardHeight))
	answer := image.NewRGBA(image.Rect(0, 0, actualWidth, actualHeight))

	for idx, row := range [][]int{selected[:8],
		selected[8:16],
		selected[16:24],
		selected[24:32],
		selected[32:]} {
		yloc := idx * cardHeight
		for pos, c := range row {
			xloc := pos * cardWidth
			if idx == 4 {
				xloc += (2 * cardWidth)
			}

			cardRect := image.Rect(xloc, yloc, xloc+cardWidth, yloc+cardHeight)

			// open the card...
			var co cardOpts
			if rand.Float64() >= revN {
				co.reversed = true
			}
			cardImg, err := deck.Image(c, cardWidth, co)
			if err != nil {
				log.Print(err)
				cardImg = image.Black
			}

			draw.Draw(answer, cardRect, cardImg, image.ZP, draw.Src)
		}
	}

	err = jpeg.Encode(w, answer, &jpeg.Options{Quality: 80})
	if err != nil {
		log.Print(err)
	}
}

// rowHandler generates an image of cards in a row, with optional
// overlap.
func rowHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Print(err)
	}

	desiredWidth, _ := strconv.Atoi(getOrElse(r.Form["width"], "600"))
	desiredCards, _ := strconv.Atoi(getOrElse(r.Form["cards"], "3"))
	desiredShowing, _ := strconv.Atoi(getOrElse(r.Form["pct"], "100"))
	desiredReversals, _ := strconv.Atoi(getOrElse(r.Form["rev"], "50"))
	desiredDeck := getOrElse(r.Form["deck"], "Lenormand")
	log.Printf("ROW: %s Cards: %d  Width: %d  Showing: %d%% Reversals: %d%%",
		desiredDeck,
		desiredCards,
		desiredWidth,
		desiredShowing,
		desiredReversals)
	revN := 1.0 - float64(desiredReversals)/100.0

	deck, err := requestDeck(desiredDeck + ".zip")
	if err != nil {
		log.Print(err)
		return
	}

	// to account for overlap, we figure out the number of
	// cards effectively showing.  Thus 3 cards showing at 100%
	// would be 1 + 1 + 1, while at 80% it would be .8 + .8 + 1
	// (since the last card is fully visible)
	showPct := float64(desiredShowing) / 100.0
	effectiveCards := 1.0 + float64(desiredCards-1)*showPct
	cardWidth := int(float64(desiredWidth) / effectiveCards)
	cardHeight := deck.CardHeight(cardWidth)
	showingWidth := int(float64(cardWidth) * showPct)

	// now, shuffle the deck
	selected, err := deck.Shuffled(desiredCards)
	if err != nil {
		log.Print(err)
		return
	}

	// now, create the image
	actualWidth := int(effectiveCards * float64(cardWidth))
	answer := image.NewRGBA(image.Rect(0, 0, actualWidth, cardHeight))
	for idx, c := range selected {
		xloc := idx * showingWidth
		cardRect := image.Rect(xloc, 0, xloc+cardWidth, cardHeight)

		// open the card...
		var co cardOpts
		if rand.Float64() >= revN {
			co.reversed = true
		}
		cardImg, err := deck.Image(c, cardWidth, co)
		if err != nil {
			log.Print(err)
			cardImg = image.Black
		}

		draw.Draw(answer, cardRect, cardImg, image.ZP, draw.Src)
	}
	err = jpeg.Encode(w, answer, &jpeg.Options{Quality: 80})
	if err != nil {
		log.Print(err)
	}
}

// celticHandler generates an image of cards in a celtic cross.
func celticHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Print(err)
	}

	desiredWidth, _ := strconv.Atoi(getOrElse(r.Form["width"], "600"))
	desiredReversals, _ := strconv.Atoi(getOrElse(r.Form["rev"], "50"))
	desiredDeck := getOrElse(r.Form["deck"], "Lenormand")
	log.Printf("CELTIC: %s Width: %d Reversals: %d%%",
		desiredDeck,
		desiredWidth,
		desiredReversals)
	revN := 1.0 - float64(desiredReversals)/100.0

	deck, err := requestDeck(desiredDeck + ".zip")
	if err != nil {
		log.Print(err)
		return
	}

	// the overall image is 7 cards wide and 4 tall:
	//  0123456
	// |  x   x|
	// |x x x x|   the "cross" part is lowered by
	// |  x   x|   half a card, relative to this pic.
	// |      x|
	cardWidth := int(float64(desiredWidth) / 7.0)
	cardSize := image.Point{cardWidth, deck.CardHeight(cardWidth)}

	// now, shuffle the deck
	selected, err := deck.Shuffled(10)
	if err != nil {
		log.Print(err)
		return
	}

	// now, create the image
	actualWidth := 7 * cardSize.X
	actualHeight := 4 * cardSize.Y
	answer := image.NewRGBA(image.Rect(0, 0, actualWidth, actualHeight))

	// nested helper function to create the card images
	getImage := func(which int, side bool) image.Image {
		var co cardOpts
		if rand.Float64() >= revN {
			co.reversed = true
		}
		co.onSide = side
		img, err := deck.Image(selected[which], cardWidth, co)
		if err != nil {
			log.Print(err)
			img = image.Black
		}
		return img
	}

	// draw the cross...
	// 1. middle
	cardImg := getImage(0, false)
	midCard := image.Point{(actualWidth - 2*cardSize.X) / 2,
		(actualHeight - cardSize.Y) / 2}
	cardRect := image.Rectangle{midCard, midCard.Add(cardSize)}
	draw.Draw(answer, cardRect, cardImg, image.ZP, draw.Src)

	// 2. crosses it
	cardImg = getImage(1, true)
	cardLoc := image.Point{(actualWidth - cardSize.X - cardSize.Y) / 2,
		(actualHeight - cardSize.X) / 2}
	cardRect = image.Rectangle{cardLoc,
		cardLoc.Add(image.Pt(cardSize.Y, cardSize.X))}
	draw.Draw(answer, cardRect, cardImg, image.ZP, draw.Src)

	// 3. below it
	cardImg = getImage(2, false)
	cardLoc = midCard.Add(image.Pt(0, cardSize.Y+cardSize.Y/3))
	cardRect = image.Rectangle{cardLoc, cardLoc.Add(cardSize)}
	draw.Draw(answer, cardRect, cardImg, image.ZP, draw.Src)

	// 4. Waning Influence
	cardImg = getImage(3, false)
	cardLoc = midCard.Sub(image.Pt(cardSize.X*2, 0))
	cardRect = image.Rectangle{cardLoc, cardLoc.Add(cardSize)}
	draw.Draw(answer, cardRect, cardImg, image.ZP, draw.Src)

	// 5. New Energy
	cardImg = getImage(4, false)
	cardLoc = midCard.Sub(image.Pt(0, cardSize.Y+cardSize.Y/3))
	cardRect = image.Rectangle{cardLoc, cardLoc.Add(cardSize)}
	draw.Draw(answer, cardRect, cardImg, image.ZP, draw.Src)

	// 6. Waxing Influence
	cardImg = getImage(5, false)
	cardLoc = midCard.Add(image.Pt(cardSize.X*2, 0))
	cardRect = image.Rectangle{cardLoc, cardLoc.Add(cardSize)}
	draw.Draw(answer, cardRect, cardImg, image.ZP, draw.Src)

	// 7 through 10...
	cardLoc = image.Point{cardSize.X * 6, cardSize.Y * 3}
	for idx := 6; idx < 10; idx++ {
		cardImg = getImage(idx, false)
		cardRect = image.Rectangle{cardLoc, cardLoc.Add(cardSize)}
		draw.Draw(answer, cardRect, cardImg, image.ZP, draw.Src)
		cardLoc = cardLoc.Sub(image.Pt(0, cardSize.Y))
	}

	err = jpeg.Encode(w, answer, &jpeg.Options{Quality: 80})
	if err != nil {
		log.Print(err)
	}
}

// houseHandler generates an image of cards around the astrological houses
func houseHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Print(err)
	}

	desiredWidth, _ := strconv.Atoi(getOrElse(r.Form["width"], "600"))
	desiredReversals, _ := strconv.Atoi(getOrElse(r.Form["rev"], "50"))
	desiredDeck := getOrElse(r.Form["deck"], "Lenormand")
	log.Printf("HOUSES: %s Width: %d Reversals: %d%%",
		desiredDeck,
		desiredWidth,
		desiredReversals)
	revN := 1.0 - float64(desiredReversals)/100.0

	deck, err := requestDeck(desiredDeck + ".zip")
	if err != nil {
		log.Print(err)
		return
	}

	// the overall image is 7 cards wide and 4 tall:
	//  0123456
	// |   a   |
	// |  b 9  |
	// | c   8 |
	// |1     7|
	// | 2   6 |
	// |  3 5  |
	// |   4   |
	cardWidth := int(float64(desiredWidth) / 7.0)
	cardSize := image.Point{cardWidth, deck.CardHeight(cardWidth)}
	halfHeight := cardSize.Y / 2
	design := []image.Point{image.Pt(0, 3*halfHeight),
		image.Pt(cardSize.X, 4*halfHeight),
		image.Pt(2*cardSize.X, 5*halfHeight),
		image.Pt(3*cardSize.X, 6*halfHeight),
		image.Pt(4*cardSize.X, 5*halfHeight),
		image.Pt(5*cardSize.X, 4*halfHeight),
		image.Pt(6*cardSize.X, 3*halfHeight),
		image.Pt(5*cardSize.X, 2*halfHeight),
		image.Pt(4*cardSize.X, halfHeight),
		image.Pt(3*cardSize.X, 0),
		image.Pt(2*cardSize.X, halfHeight),
		image.Pt(1*cardSize.X, 2*halfHeight)}

	// now, shuffle the deck
	selected, err := deck.Shuffled(12)
	if err != nil {
		log.Print(err)
		return
	}

	// now, create the image
	actualWidth := 7 * cardSize.X
	actualHeight := 4 * cardSize.Y
	answer := image.NewRGBA(image.Rect(0, 0, actualWidth, actualHeight))

	// nested helper function to create the card images
	getImage := func(which int) image.Image {
		var co cardOpts
		if rand.Float64() >= revN {
			co.reversed = true
		}
		co.onSide = false
		img, err := deck.Image(selected[which], cardWidth, co)
		if err != nil {
			log.Print(err)
			img = image.Black
		}
		return img
	}

	for idx, v := range design {
		cardImg := getImage(idx)
		cardRect := image.Rectangle{v, v.Add(cardSize)}
		draw.Draw(answer, cardRect, cardImg, image.ZP, draw.Src)
	}

	err = jpeg.Encode(w, answer, &jpeg.Options{Quality: 80})
	if err != nil {
		log.Print(err)
	}
}
