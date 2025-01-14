package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/keybraker/mediarizer-2/hash"
)

var (
	inputPath         *string
	outputPath        *string
	duplicateStrategy *string
	moveUnknown       *bool
	geoLocation       *bool
	fileTypesString   *string
	organisePhotos    *bool
	organiseVideos    *bool
	format            *string
	showHelp          *bool
	verbose           *bool
	showVersion       *bool

	InfoLogger    *log.Logger
	VerboseLogger *log.Logger
	WarningLogger *log.Logger
	ErrorLogger   *log.Logger
)

func init() {
	inputPath = flag.String("input", "", "Path to source file or directory")
	outputPath = flag.String("output", "", "Path to destination directory")
	duplicateStrategy = flag.String("duplicate", "move", "Duplication handling, default \"move\" (move, skip, delete)")
	moveUnknown = flag.Bool("unknown", true, "Move files with no metadata to undetermined folder")
	geoLocation = flag.Bool("location", false, "Organize files based on their geo location")
	fileTypesString = flag.String("types", "", "Comma separated file extensions to organize (.jpg, .png, .gif, .mp4, .avi, .mov, .mkv)")
	organisePhotos = flag.Bool("photo", true, "Organise only photos")
	organiseVideos = flag.Bool("video", true, "Organise only videos")
	format = flag.String("format", "word", "Naming format for month folders, default \"word\" (word, number, combined)")
	showHelp = flag.Bool("help", false, "Display usage guide")
	verbose = flag.Bool("verbose", false, "Display progress information in console")
	showVersion = flag.Bool("version", false, "Display version information")

	InfoLogger = log.New(os.Stdout, "\033[1m\033[34minfo\033[0m:\t", log.Lmsgprefix)
	VerboseLogger = log.New(os.Stdout, "\033[1m\033[36mverbose\033[0m:\t", log.Ldate|log.Ltime)
	WarningLogger = log.New(os.Stdout, "\033[1m\033[33mwarn\033[0m:\t", log.Ldate|log.Ltime)
	ErrorLogger = log.New(os.Stdout, "\033[1m\033[31merror\033[0m:\t", log.Ldate|log.Ltime)
}

func logger(loggerType string, message string) {
	switch loggerType {
	case "info":
		InfoLogger.Println(message)
	case "verbose":
		if *verbose {
			VerboseLogger.Println(message)
		}
	case "warning":
		WarningLogger.Println(message)
	case "error":
		ErrorLogger.Println(message)
	case "fatal":
		ErrorLogger.Fatal(message)
	}

}

func flagProcessor() []string {
	if *showHelp {
		displayHelp()
		os.Exit(0)
	}

	if *showVersion {
		fmt.Println("v1.0.0")
		os.Exit(0)
	}

	if *inputPath == "" || *outputPath == "" {
		logger("fatal", "input and output paths are mandatory")
	}

	var fileTypes []string
	if *fileTypesString != "" {
		isValidType := false
		fileTypes = strings.Split(*fileTypesString, ",")

		for i := range fileTypes {
			if isPhoto(strings.ToLower(fileTypes[i])) {
				isValidType = true
				break
			}
			if isVideo(strings.ToLower(fileTypes[i])) {
				isValidType = true
				break
			}
		}

		if !isValidType {
			logger("fatal", "one or more file types supplied are invalid")
		}
	}

	if *geoLocation {
		loadFeatureCollection()
	}

	return fileTypes
}

func main() {
	flag.Parse()
	fileTypes := flagProcessor()

	sourcePath := filepath.Clean(*inputPath)
	destinationPath := filepath.Clean(*outputPath)

	if sourcePath == "" || destinationPath == "" {
		logger("fatal", "input and output paths must be supplied")
	}

	sourceDrive := filepath.VolumeName(sourcePath)
	destinationDrive := filepath.VolumeName(destinationPath)

	if sourceDrive == "" || destinationDrive == "" {
		logger("fatal", "input and output paths must be on drives")
	} else if sourceDrive == destinationDrive {
		logger("fatal", "input and output paths must on the same drive")
	}

	fileQueue := make(chan FileInfo, 100)
	defer close(fileQueue)
	infoQueue := make(chan string, 50)
	defer close(infoQueue)
	warnQueue := make(chan string, 10)
	defer close(warnQueue)
	errorQueue := make(chan error, 50)
	defer close(errorQueue)
	done := make(chan struct{})

	go infoHandler(infoQueue)
	go warnHandler(warnQueue)
	go errorHandler(errorQueue)

	logger("info", "Counting files to move...")
	totalFiles := countFiles(sourcePath, fileTypes, *organisePhotos, *organiseVideos)
	logger("info", "Completed.")

	hashCache := &sync.Map{}

	logger("info", "Creating file hash map...")
	fileHashMap, err := hash.HashImagesInPath(destinationPath, hashCache)
	if err != nil {
		logger("info", "Failed to create file has map.")
		logger("fatal", err.Error())
	}
	logger("info", "Completed.")

	go creator(
		sourcePath,
		fileQueue,
		infoQueue,
		warnQueue,
		errorQueue,
		*geoLocation,
		*moveUnknown,
		fileTypes,
		*organisePhotos,
		*organiseVideos,
		*duplicateStrategy,
		fileHashMap,
		hashCache,
	)

	go consumer(
		destinationPath,
		fileQueue,
		infoQueue,
		warnQueue,
		errorQueue,
		*geoLocation,
		*format,
		*verbose,
		totalFiles,
		*duplicateStrategy,
		done,
	)

	<-done

	logger("info", strconv.Itoa(totalFiles)+" files processed.")
}

func errorHandler(errorQueue chan error) {
	for err := range errorQueue {
		logger("error", fmt.Sprintf("%v\n", err))
	}
}

func warnHandler(warnQueue chan string) {
	for warning := range warnQueue {
		logger("warn", fmt.Sprintf("%v\n", warning))
	}
}

func infoHandler(infoQueue chan string) {
	for message := range infoQueue {
		logger("info", fmt.Sprintf("%v\n", message))
	}
}

func displayHelp() {
	flag.PrintDefaults()
}

func arrayContains(stringArray []string, stringCandidate string) bool {
	for _, string := range stringArray {
		if string == stringCandidate {
			return true
		}
	}

	return false
}

func countFiles(rootPath string, fileTypes []string, organisePhotos bool, organiseVideos bool) int {
	count := 0

	filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))

			if organisePhotos && isPhoto(ext) && (len(fileTypes) == 0 || arrayContains(fileTypes, ext)) {
				count++
			} else if organiseVideos && isVideo(ext) && (len(fileTypes) == 0 || arrayContains(fileTypes, ext)) {
				count++
			}
		}

		return nil
	})

	return count
}
