package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/attic-labs/noms/clients/util"
	"github.com/attic-labs/noms/d"
	"github.com/attic-labs/noms/datas"
	"github.com/attic-labs/noms/dataset"
	"github.com/attic-labs/noms/types"
)

var (
	inputID  = flag.String("in", "", "dataset to pull data from.")
	outputID = flag.String("out", "", "dataset to store data in.")
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	flag.Usage = func() {
		fmt.Printf("Usage: %s -ldb=/path/to/db -in=<dataset> -out=<dataset>\n\n", os.Args[0])
		flag.PrintDefaults()
	}

	flags := datas.NewFlags()
	flag.Parse()

	ds, ok := flags.CreateDataStore()
	if !ok || *inputID == "" || *outputID == "" {
		flag.Usage()
		return
	}
	defer ds.Close()

	err := d.Try(func() {
		if util.MaybeStartCPUProfile() {
			defer util.StopCPUProfile()
		}
		inputDataset := dataset.NewDataset(ds, *inputID)
		outputDataset := dataset.NewDataset(ds, *outputID)

		imp := inputDataset.Head().Value().(Import)
		v := imp.Companies().TargetValue(ds)

		type entry struct {
			key           Key
			roundRaiseDef RoundRaiseDef
		}

		c := make(chan entry, 1024)

		mapOfSets := MapOfRefOfKeyToSetOfRoundRaiseDef{}

		addTimeRounds := func(tn int64, roundRaiseDef RoundRaiseDef) {
			t := time.Unix(tn, 0)
			year := int32(t.Year())
			yk := NewKey(ds).SetYear(year)
			c <- entry{yk, roundRaiseDef}

			q := timeToQuarter(t)
			qk := NewKey(ds).SetQuarter(QuarterDef{Year: year, Quarter: q}.New(ds))
			c <- entry{qk, roundRaiseDef}
		}

		go func() {
			v.IterAllP(64, func(permalink string, r RefOfCompany) {
				company := r.TargetValue(ds)
				categoryList := company.CategoryList()
				regionKey := NewKey(ds).SetRegion(company.Region())
				company.Rounds().IterAll(func(r RefOfRound) {
					round := r.TargetValue(ds)
					roundRaiseDef := RoundRaiseDef{
						Raised:  round.RaisedAmountUsd(),
						Details: r.TargetRef(),
					}
					categoryList.IterAllP(64, func(category string) {
						key := NewKey(ds).SetCategory(category)
						c <- entry{key, roundRaiseDef}
					})

					c <- entry{regionKey, roundRaiseDef}
					addTimeRounds(round.FundedAt(), roundRaiseDef)

					roundType := classifyRoundType(round)
					roundTypeKey := NewKey(ds).SetRoundType(roundType)
					c <- entry{roundTypeKey, roundRaiseDef}
				})
			})

			close(c)
		}()

		for e := range c {
			key := e.key
			roundRaiseDef := e.roundRaiseDef
			keyRef := key.Ref()
			setDef := mapOfSets[keyRef]
			if setDef == nil {
				setDef = SetOfRoundRaiseDef{}
			}
			setDef[roundRaiseDef] = true
			mapOfSets[keyRef] = setDef
		}

		mapOfRefs := MapOfRefOfKeyToRefOfSetOfRoundRaiseDef{}
		for keyRef, set := range mapOfSets {
			setRef := types.WriteValue(set.New(ds), ds)
			mapOfRefs[keyRef] = setRef
		}

		output := mapOfRefs.New(ds)
		_, ok = outputDataset.Commit(output)
		d.Exp.True(ok, "Could not commit due to conflicting edit")

		util.MaybeWriteMemProfile()
	})
	if err != nil {
		log.Fatal(err)
	}
}

func classifyRoundType(round Round) RoundTypeEnum {
	if round.FundingRoundType() == "seed" {
		return Seed
	}
	switch round.FundingRoundCode() {
	case "A":
		return SeriesA
	case "B":
		return SeriesB
	case "C":
		return SeriesC
	case "D":
		return SeriesD
	case "E":
		return SeriesE
	case "F":
		return SeriesF
	case "G":
		return SeriesG
	case "H":
		return SeriesH
	default:
		return UnknownRoundType
	}
}

func timeToQuarter(t time.Time) QuarterEnum {
	switch t.Month() {
	case time.January, time.February, time.March:
		return Q1
	case time.April, time.May, time.June:
		return Q2
	case time.July, time.August, time.September:
		return Q3
	case time.October, time.November, time.December:
		return Q4
	}
	panic("unreachable")
}