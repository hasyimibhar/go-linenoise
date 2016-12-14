package linenoise

type LineNoise struct {
}

type CompletionCallback func(line string, cpl *Completion)
type HintsCallback func(line string, color int, bold bool) string

// Readline displays the specified prompt to the user, and reads from stdin.
// It returns the line written by the user.
func (l *LineNoise) Readline(prompt string) string {
	return ""
}

// SetMultiline sets whether multiline mode is enabled.
func (l *LineNoise) SetMultiline(enabled bool) {

}

// AddHistory adds the line to the history.
// If the history exceeds the maximum length as set using SetHistoryMaxLength,
// the oldest line will be truncated.
func (l *LineNoise) AddHistory(line string) {

}

// SetHistoryMaxLength sets the maximum length of the history.
func (l *LineNoise) SetHistoryMaxLength(length int) {

}

// WriteHistoryToFile writes the history to a file.
func (l *LineNoise) WriteHistoryToFile(filename string) error {
	return nil

}

// LoadHistoryFromFile loads the history from a file.
func (l *LineNoise) LoadHistoryFromFile(filename string) error {
	return nil
}

// SetCompletionCallback sets the completion callback.
func (l *LineNoise) SetCompletionCallback(cb CompletionCallback) {

}

// SetHintsCallback sets the hints callback.
func (l *LineNoise) SetHintsCallback(cb HintsCallback) {

}

// Clear clears the screen.
func (l *LineNoise) Clear() {

}

type Completion struct {
}

// Add adds the specified line to the completion list.
func (c *Completion) Add(line string) {

}
