/*
management-interface - Web based management of Raspberry Pis over WiFi
Copyright (C) 2019, The Cacophony Project

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.
*/

package managementinterface

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/TheCacophonyProject/audiobait/audiofilelibrary"
	"github.com/TheCacophonyProject/audiobait/playlist"
	goconfig "github.com/TheCacophonyProject/go-config"
	"github.com/gobuffalo/packr"
	"github.com/gorilla/mux"
)

var audioBox = packr.NewBox("./audio")

// Return recent log entries from the audiobait process
func getAudiobaitLogEntries() string {

	logEntries := make([]string, 0)
	out, err := exec.Command("/bin/journalctl", "-u", "audiobait", "--no-pager", "-n", "100").Output()
	if err != nil {
		log.Println("Could not get audio bait logging info:", err)
		return "Could not get audio bait logging info."
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) <= 1 {
		// Didn't get any useful output.
		log.Println("Could not get audio bait logging info:", err)
		return "Could not get audio bait logging info."
	} else if len(lines) >= 2 && strings.Contains(strings.ToUpper(lines[1]), "NO ENTRIES") {
		// There are no audio bait log entries to show.
		return "There are no audio bait log entries."
	}

	// Separate log entries. The first line contains a sort of header that we don't want.
	for _, line := range lines[1:] {
		if len(line) > 0 {
			logEntries = append(logEntries, line)
		}
	}

	// Show newest log entries first.  Note that the --reverse option for the journalctl command above does
	// not seem to work with the -u option.  So doing it here.
	reverse(logEntries)

	// Now combine back into 1 string.
	outputText := ""
	for _, line := range logEntries {
		outputText += line + "\n"
	}

	return outputText

}

func isAudiobaitRunning() bool {

	// Run systemctl commands to see if the audiobait process is running.
	out, err := exec.Command("/bin/systemctl", "is-active", "audiobait").Output()
	if err != nil {
		return false
	}
	active := strings.ToUpper(strings.TrimSpace(string(out))) == "ACTIVE"

	out, err = exec.Command("/bin/systemctl", "is-enabled", "audiobait").Output()
	if err != nil {
		return false
	}
	enabled := strings.ToUpper(strings.TrimSpace(string(out))) == "ENABLED"

	if active && enabled {
		return true
	}
	return false

}

// Return the time part of the time.Time struct as a string.
func extractTimeOfDayAsString(t playlist.TimeOfDay) string {
	return t.Format("3:04PM")
}

type scheduleResponse struct {
	Schedule playlist.Schedule
}

// These next 4 structs are used to put the audiobait data into a format that
// makes it easy to display
type soundDisplayInfo struct {
	SoundFileDisplayText string // 2 fields are needed because the sound ID can sometimes be set to "same" if the same sound is to be played as before.
	SoundFileName        string
	Volume               int
	Wait                 int
}
type soundDisplayCombo struct {
	From      string
	Every     int
	Until     string
	SoundInfo []soundDisplayInfo
}
type soundDisplaySchedule struct {
	Description   string
	Timestamp     string
	ControlNights int
	PlayNights    int
	StartDay      int
	Combos        []soundDisplayCombo
}
type audiobaitResponse struct {
	Running      bool
	Schedule     soundDisplaySchedule
	Message      string
	ErrorMessage string
}

func loadScheduleFromDisk(audioDirectory string) (*playlist.Schedule, error) {
	filename := filepath.Join(audioDirectory, scheduleFilename)
	jsonData, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var sr scheduleResponse
	if err = json.Unmarshal(jsonData, &sr); err != nil {
		return nil, err
	}
	return &sr.Schedule, nil
}

func restartAudiobaitService() bool {
	_, err := exec.Command("systemctl", "restart", "audiobait").Output()
	if err != nil {
		return false
	}
	return true
}

func getScheduleData(resp *audiobaitResponse, conf *goconfig.Config) soundDisplaySchedule {

	// Get the audiobait schedule from disk.
	audio := goconfig.DefaultAudio()
	if err := conf.Unmarshal(goconfig.AudioKey, &audio); err != nil {
		resp.ErrorMessage = errorMessage(err)
		return soundDisplaySchedule{}
	}

	schedule, err := loadScheduleFromDisk(audio.Dir)
	if err != nil {
		resp.ErrorMessage = errorMessage(err)
		return soundDisplaySchedule{}
	}

	// Put the schedule data into a format which makes rendering on html page easy.
	displaySchedule := soundDisplaySchedule{
		Description:   schedule.Description,
		ControlNights: schedule.ControlNights,
		PlayNights:    schedule.PlayNights,
		StartDay:      schedule.StartDay,
	}

	// Get the timestamp for the schedule so the user can see when the schedule was last downloaded.
	filename := filepath.Join(audio.Dir, scheduleFilename)
	file, err := os.Stat(filename)
	if err != nil {
		displaySchedule.Timestamp = "Unknown."
	} else {
		displaySchedule.Timestamp = file.ModTime().Format("3:04PM, Monday January 2 2006")
	}

	// The schedule file provides us with audio file IDs.  To get the file names, we
	// need to load them off of disk.
	audioLibraryLoaded := true
	audioLibrary, err := audiofilelibrary.OpenLibrary(audio.Dir, scheduleFilename)
	if err != nil {
		audioLibraryLoaded = false
	}

	for _, combo := range schedule.Combos {
		displayCombo := soundDisplayCombo{
			From:  extractTimeOfDayAsString(combo.From),
			Every: combo.Every / 60,
			Until: extractTimeOfDayAsString(combo.Until),
		}
		for j := 0; j < len(combo.Sounds); j++ {
			displayInfo := soundDisplayInfo{
				SoundFileDisplayText: combo.Sounds[j], // Set to the ID (type == string) from the schedule intially.  This can be the text "same" if the same 2 sounds follow eachother.
				Volume:               combo.Volumes[j],
			}
			// Try and get file name off of disk.
			if audioLibraryLoaded {
				ID, err := strconv.Atoi(combo.Sounds[j])
				if err == nil {
					fileName, exists := audioLibrary.GetFileNameOnDisk(ID)
					if exists {
						// We have the file name so display this on the html page.
						displayInfo.SoundFileDisplayText = fileName
						// And store it in this field so we have it when we want to play the file.
						displayInfo.SoundFileName = fileName
					}
				} else {
					if j > 0 && strings.ToUpper(combo.Sounds[j]) == "SAME" {
						// This is the case where we need to play the same sound again.
						displayInfo.SoundFileDisplayText = "Same"
						displayInfo.SoundFileName = displayCombo.SoundInfo[j-1].SoundFileName
					}
				}

			}
			if j < len(combo.Sounds)-1 {
				displayInfo.Wait = combo.Waits[j+1] / 60
			}
			displayCombo.SoundInfo = append(displayCombo.SoundInfo, displayInfo)
		}
		displaySchedule.Combos = append(displaySchedule.Combos, displayCombo)
	}

	return displaySchedule

}

// AudiobaitHandlerGen is a wrapper for the AudiobaitHandler function.
func AudiobaitHandlerGen(conf *goconfig.Config) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		AudiobaitHandler(w, r, conf)
	}
}

// AudiobaitHandler shows the status and details of audiobait being played on the device.
func AudiobaitHandler(w http.ResponseWriter, r *http.Request, conf *goconfig.Config) {

	// Creat our response
	resp := &audiobaitResponse{}

	switch r.Method {

	case "POST":

		// Restart the audiobait service
		if restartAudiobaitService() {
			resp.Running = true
			resp.Message = "Audiobait service successfully restarted."
		} else {
			resp.ErrorMessage = "Could not restart audio bait service."
		}

	case "GET", "":

		resp.Running = isAudiobaitRunning()

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}

	// Get the schedule data
	resp.Schedule = getScheduleData(resp, conf)
	tmpl.ExecuteTemplate(w, "audiobait.html", *resp)

}

// AudiobaitLogEntriesHandler returns recent log entries for the audiobait process
func AudiobaitLogEntriesHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusOK)
	response := make(map[string]string)

	// Get the log entries that the audiobait program has output recently.
	logEntries := getAudiobaitLogEntries()
	response["result"] = strings.TrimSpace(logEntries)

	json.NewEncoder(w).Encode(response)

}

// AudiobaitSoundsHandlerGen is a wrapper for the AudiobaitSoundsHandler function.
func AudiobaitSoundsHandlerGen(conf *goconfig.Config) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		AudiobaitSoundsHandler(w, r, conf)
	}
}

// AudiobaitSoundsHandler attempts to play a sound on connected speaker(s) at the volume set in the schedule.
func AudiobaitSoundsHandler(w http.ResponseWriter, r *http.Request, conf *goconfig.Config) {

	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	response := make(map[string]string)

	// Extract sound file name
	fileName := mux.Vars(r)["fileName"]
	volume, _ := strconv.Atoi(mux.Vars(r)["volume"])

	// Get audiobait directory
	audio := goconfig.DefaultAudio()
	if err := conf.Unmarshal(goconfig.AudioKey, &audio); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("audio output failed: %v", err)
		response["result"] = fmt.Sprintf("Error: %v.", err.Error())
		json.NewEncoder(w).Encode(response)
		return
	}

	// Play the sound.
	if output, err := playAudioBaitSound(audio, fileName, volume); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("audio output failed: %v", err)
		response["result"] = fmt.Sprintf("Error: %v. Output:\n%s", err.Error(), string(output))
	} else {
		w.WriteHeader(http.StatusOK)
		response["result"] = string(output)
	}

	// Encode data to be sent back to html.
	json.NewEncoder(w).Encode(response)
}

// Play the specified sound on the speaker.
func playAudioBaitSound(audio goconfig.Audio, fileName string, volume int) ([]byte, error) {

	var cmd *exec.Cmd
	var soundFile []byte

	// Set volume
	err := setVolume(volume, audio)
	if err != nil {
		return nil, fmt.Errorf("unable to set the volume: %v", err)
	}

	// Either we're playing the default test sound, or a sound from the schedule.
	if fileName == "test.wav" {
		soundFile = audioBox.Bytes("test.wav")
		if soundFile == nil {
			return nil, errors.New("unable to load test audio")
		}
		cmd = exec.Command("play", "-t", "wav", "--norm=-3", "-q", "-")
	} else {
		// It's a sound specified in the schedule. Load the file from the audiobait directory.
		soundFile, err = ioutil.ReadFile(filepath.Join(audio.Dir, fileName))
		if soundFile == nil || err != nil {
			return nil, errors.New("unable to load audio bait sound file: " + fileName)
		}
		// Get the file type e.g. .wav, .mp3 etc.
		fileType := filepath.Ext(fileName)
		if fileType == "" {
			fileType = "wav"
		}
		cmd = exec.Command("play", "-t", fileType, "--norm=-3", "-q", "-")
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("unable to play audio: %v", err)
	}

	go func() {
		defer stdin.Close()
		w := bufio.NewWriter(stdin)
		if _, err := w.Write(soundFile); err != nil {
			log.Printf("unable to pass audio: %v", err)
		}
	}()

	return cmd.CombinedOutput()
}

// Set the volume on the sound card.
func setVolume(volume int, audio goconfig.Audio) error {
	cmd := exec.Command(
		"amixer",
		"-c", fmt.Sprint(audio.Card),
		"sset",
		audio.VolumeControl,
		fmt.Sprintf("%d%%", volume*10),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("volume set failed: %v\noutput:\n%s", err, out)
	}
	return nil
}
