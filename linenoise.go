package linenoise

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/creack/termios/raw"
	"github.com/mattn/go-isatty"
)

var unsupportedTerms = []string{"dumb", "cons25", "emacs"}

const (
	MaxLine          = 4096
	HistoryMaxLength = 100

	historyNext = 0
	historyPrev = 1

	CTRL_A    = 1
	CTRL_B    = 2
	CTRL_C    = 3
	CTRL_D    = 4
	CTRL_E    = 5
	CTRL_F    = 6
	CTRL_H    = 8
	TAB       = 9
	CTRL_K    = 11
	CTRL_L    = 12
	ENTER     = 13
	CTRL_N    = 14
	CTRL_P    = 16
	CTRL_T    = 20
	CTRL_U    = 21
	CTRL_W    = 23
	ESC       = 27
	BACKSPACE = 127
)

type LineNoise struct {
	in           io.Reader
	out          io.Writer
	buf          []byte
	bufLen       uint
	prompt       string
	plen         uint
	pos          uint
	oldPos       uint
	editLen      uint
	cols         uint
	maxRows      uint
	historyIndex uint

	rawMode    bool
	termios    *raw.Termios
	mlMode     bool
	historyLen uint
	history    []string
}

func New() *LineNoise {
	return &LineNoise{
		history: []string{},
	}
}

type CompletionCallback func(line string, cpl *Completion)
type HintsCallback func(line string, color int, bold bool) string

// Readline displays the specified prompt to the user, and reads from stdin.
// It returns the line written by the user.
func (l *LineNoise) Readline(prompt string) (string, error) {
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return l.noTTY(), nil
	}

	if isUnsupportedTerm() {
		log.Println("unsupported")
		f := bufio.NewWriter(os.Stdout)
		f.Write([]byte(prompt))
		f.Flush()

		r := bufio.NewReader(os.Stdin)
		line, _ := r.ReadString('\n')
		return line, nil
	} else {
		buf := make([]byte, MaxLine)
		count := l.raw(buf, prompt)
		if count == -1 {
			return "", io.EOF
		}
		return string(buf[:l.editLen]), nil
	}
}

func (l *LineNoise) noTTY() string {
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	return line
}

func (l *LineNoise) raw(buf []byte, prompt string) int {
	if len(buf) == 0 {
		return -1
	}

	if l.enableRawMode(os.Stdin.Fd()) == -1 {
		return -1
	}

	count := l.edit(os.Stdin, os.Stdout, buf, prompt)
	l.disableRawMode(os.Stdin.Fd())

	fmt.Println("")

	return count
}

func (l *LineNoise) enableRawMode(fd uintptr) int {
	var err error
	if l.termios, err = raw.TcGetAttr(fd); err != nil {
		return -1
	}
	if _, err = raw.MakeRaw(fd); err != nil {
		return -1
	}

	l.rawMode = true
	return 0
}

func (l *LineNoise) disableRawMode(fd uintptr) {
	if !l.rawMode {
		return
	}

	raw.TcSetAttr(fd, l.termios)
	l.rawMode = false
}

func (l *LineNoise) edit(in io.Reader, out io.Writer, buf []byte, prompt string) int {
	l.in = in
	l.out = out
	l.buf = buf
	l.bufLen = 0
	l.prompt = prompt
	l.plen = uint(len(prompt))
	l.oldPos = 0
	l.pos = 0
	l.editLen = 0
	l.cols = l.getColumns(in, out)
	l.maxRows = 0
	l.historyIndex = 0

	l.buf[0] = 0
	l.bufLen--

	/* The latest history entry is always our current buffer, that
	 * initially is just an empty string. */
	l.AddHistory("")

	if _, err := l.out.Write([]byte(prompt)); err != nil {
		return -1
	}

	for {
		r := bufio.NewReader(l.in)
		c, err := r.ReadByte()
		if err != nil {
			return int(l.editLen)
		}

		// TODO: Implement autocomplete

		/* Only autocomplete when the callback is set. It returns < 0 when
		 * there was an error reading from fd. Otherwise it will return the
		 * character that should be handled next. */
		// if (c == 9 && completionCallback != NULL) {
		//     c = completeLine(&l);
		//     /* Return on errors */
		//     if (c < 0) return l.editLen;
		//     /* Read next character when 0 */
		//     if (c == 0) continue;
		// }

		switch c {
		case ENTER: /* enter */
			// l.historyLen--
			// if (l.mlMode) {
			// 	l.editMoveEnd()
			// }
			// if (hintsCallback) {
			//     /* Force a refresh without hints to leave the previous
			//      * line as the user typed it after a newline. */
			//     linenoiseHintsCallback *hc = hintsCallback;
			//     hintsCallback = NULL;
			//     refreshLine(&l);
			//     hintsCallback = hc;
			// }
			return int(l.editLen)

		case CTRL_C:
			return -1

		case BACKSPACE, 8:
			l.editBackspace()
			break

		case CTRL_D: /* ctrl-d, remove char at right of cursor, or if the
			   line is empty, act as end-of-file. */
			if l.editLen > 0 {
				l.editDelete()
				break
			} else {
				// l.historyLen--
				return -1
			}

		case CTRL_T: /* ctrl-t, swaps current character with previous. */
			if l.pos > 0 && l.pos < l.editLen {
				aux := buf[l.pos-1]
				buf[l.pos-1] = buf[l.pos]
				buf[l.pos] = aux
				if l.pos != l.editLen-1 {
					l.pos++
				}
				l.refreshLine()
			}
			break

		case CTRL_B: /* ctrl-b */
			l.editMoveLeft()
			break

		case CTRL_F: /* ctrl-f */
			l.editMoveRight()
			break

		case CTRL_P: /* ctrl-p */
			l.editHistoryNext(historyPrev)
			break

		case CTRL_N: /* ctrl-n */
			l.editHistoryNext(historyNext)
			break

		case ESC: /* escape sequence */
			/* Read the next two bytes representing the escape sequence.
			 * Use two calls to handle slow terminals returning the two
			 * chars at different times. */

			seq0, err := r.ReadByte()
			if err != nil {
				break
			}
			seq1, err := r.ReadByte()
			if err != nil {
				break
			}

			/* ESC [ sequences. */
			if seq0 == '[' {
				if seq1 >= '0' && seq1 <= '9' {
					/* Extended escape, read additional byte. */
					seq2, err := r.ReadByte()
					if err != nil {
						break
					}
					if seq2 == '~' {
						switch seq1 {
						case '3': /* Delete key. */
							l.editDelete()
							break
						}
					}
				} else {
					switch seq1 {
					case 'A': /* Up */
						l.editHistoryNext(historyPrev)
						break
					case 'B': /* Down */
						l.editHistoryNext(historyNext)
						break
					case 'C': /* Right */
						l.editMoveRight()
						break
					case 'D': /* Left */
						l.editMoveLeft()
						break
					case 'H': /* Home */
						l.editMoveHome()
						break
					case 'F': /* End*/
						l.editMoveEnd()
						break
					}
				}
			} else if seq0 == 'O' {
				/* ESC O sequences. */
				switch seq1 {
				case 'H': /* Home */
					l.editMoveHome()
					break
				case 'F': /* End*/
					l.editMoveEnd()
					break
				}
			}
			break
		default:
			if l.editInsert(c) != 0 {
				return -1
			}
			break

		case CTRL_U: /* Ctrl+u, delete the whole line. */
			l.buf[0] = 0
			l.pos = 0
			l.editLen = 0
			l.refreshLine()
			break

		case CTRL_K: /* Ctrl+k, delete from current to end of line. */
			buf[l.pos] = 0
			l.editLen = l.pos
			l.refreshLine()
			break

		case CTRL_A: /* Ctrl+a, go to the start of the line */
			l.editMoveHome()
			break

		case CTRL_E: /* ctrl+e, go to the end of the line */
			l.editMoveEnd()
			break

		case CTRL_L: /* ctrl+l, clear screen */
			l.ClearScreen()
			l.refreshLine()
			break

		case CTRL_W: /* ctrl+w, delete previous word */
			l.editDeletePrevWord()
			break
		}
	}
	return int(l.editLen)
}

/* Insert the character 'c' at cursor current position.
 *
 * On error writing to the terminal -1 is returned, otherwise 0. */
func (l *LineNoise) editInsert(c byte) int {
	if l.editLen < l.bufLen {
		if l.editLen == l.pos {
			l.buf[l.pos] = c
			l.pos++
			l.editLen++
			l.buf[l.editLen] = 0
			if !l.mlMode && l.plen+l.editLen < l.cols /*&& !hintsCallback*/ {
				/* Avoid a full update of the line in the
				 * trivial case. */
				if _, err := l.out.Write([]byte{c}); err != nil {
					return -1
				}
			} else {
				l.refreshLine()
			}
		} else {
			copy(l.buf[l.pos+1:], l.buf[l.pos:l.editLen])
			l.buf[l.pos] = c
			l.editLen++
			l.pos++
			l.buf[l.editLen] = 0
			l.refreshLine()
		}
	}
	return 0
}

/* Move cursor on the left. */
func (l *LineNoise) editMoveLeft() {
	if l.pos > 0 {
		l.pos--
		l.refreshLine()
	}
}

/* Move cursor on the right. */
func (l *LineNoise) editMoveRight() {
	if l.pos != l.editLen {
		l.pos++
		l.refreshLine()
	}
}

/* Move cursor to the start of the line. */
func (l *LineNoise) editMoveHome() {
	if l.pos != 0 {
		l.pos = 0
		l.refreshLine()
	}
}

/* Move cursor to the end of the line. */
func (l *LineNoise) editMoveEnd() {
	if l.pos != l.editLen {
		l.pos = l.editLen
		l.refreshLine()
	}
}

/* Substitute the currently edited line with the next or previous history
 * entry as specified by 'dir'. */
func (l *LineNoise) editHistoryNext(dir int) {
	// if l.historyLen > 1 {
	//     /* Update the current history entry before to
	//      * overwrite it with the next one. */
	//     free(history[history_len - 1 - l.history_index]);
	//     history[history_len - 1 - l.history_index] = strdup(l.buf);
	//     /* Show the new entry */
	//     l.history_index += (dir == historyPrev) ? 1 : -1;
	//     if l.history_index < 0 {
	//         l.history_index = 0;
	//         return;
	//     } else if l.history_index >= history_len {
	//         l.history_index = history_len-1;
	//         return;
	//     }
	//     strncpy(l.buf,history[history_len - 1 - l.history_index],l.bufLen)
	//     l.buf[l.bufLen-1] = 0
	//     l.editLen = len(l.buf)
	//     l.pos = len(l.buf)
	//     l.refreshLine()
	// }
}

/* Delete the character at the right of the cursor without altering the cursor
 * position. Basically this is what happens with the "Delete" keyboard key. */
func (l *LineNoise) editDelete() {
	if l.editLen > 0 && l.pos < l.editLen {
		copy(l.buf[l.pos:], l.buf[l.pos+1:l.editLen])
		l.editLen--
		l.buf[l.editLen] = 0
		l.refreshLine()
	}
}

/* Backspace implementation. */
func (l *LineNoise) editBackspace() {
	if l.pos > 0 && l.editLen > 0 {
		copy(l.buf[l.pos-1:], l.buf[l.pos:l.editLen])
		l.pos--
		l.editLen--
		l.buf[l.editLen] = 0
		l.refreshLine()
	}
}

/* Delete the previosu word, maintaining the cursor at the start of the
 * current word. */
func (l *LineNoise) editDeletePrevWord() {
	oldPos := l.pos

	for l.pos > 0 && l.buf[l.pos-1] == ' ' {
		l.pos--
	}
	for l.pos > 0 && l.buf[l.pos-1] != ' ' {
		l.pos--
	}
	diff := oldPos - l.pos
	copy(l.buf[l.pos:], l.buf[oldPos:l.editLen+1])
	l.editLen -= diff
	l.refreshLine()
}

func (l *LineNoise) refreshLine() {
	if l.mlMode {
		l.refreshMultiLine()
	} else {
		l.refreshSingleLine()
	}
}

func (l *LineNoise) refreshSingleLine() {
	plen := len(l.prompt)
	bi := uint(0)
	editLen := l.editLen
	pos := l.pos

	for uint(plen)+pos >= l.cols {
		bi++
		editLen--
		pos--
	}
	for uint(plen)+editLen > l.cols {
		editLen--
	}

	ab := []byte{}

	/* Cursor to left edge */
	ab = append(ab, byte('\r'))

	/* Write the prompt and the current buffer content */
	ab = append(ab, []byte(l.prompt)...)
	ab = append(ab, l.buf[bi:bi+editLen]...)

	/* Show hits if any. */
	l.refreshShowHints(ab, uint(plen))

	/* Erase to right */
	ab = append(ab, []byte("\x1b[0K")...)

	/* Move cursor to original position. */
	s := fmt.Sprintf("\r\x1b[%dC", int(pos)+plen)
	ab = append(ab, []byte(s)...)

	l.out.Write(ab)
}

func (l *LineNoise) refreshMultiLine() {
}

func (l *LineNoise) refreshShowHints(ab []byte, plen uint) {

}

type winSize struct {
	row, col       uint16
	xpixel, ypixel uint16
}

func (l *LineNoise) getColumns(in io.Reader, out io.Writer) uint {
	var ws winSize
	ok, _, _ := syscall.Syscall(syscall.SYS_IOCTL, uintptr(syscall.Stdout),
		syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))

	if int(ok) == -1 || ws.col == 0 {
		start := l.getCursorPosition(in, out)
		if start == -1 {
			return 80
		}

		/* Go to right margin and get position. */
		if n, err := out.Write([]byte("\x1b[999C")); n != 6 || err != nil {
			return 80
		}
		cols := l.getCursorPosition(in, out)
		if cols == -1 {
			return 80
		}

		/* Restore position. */
		if cols > start {
			out.Write([]byte(fmt.Sprintf("\x1b[%dD", cols-start)))
		}

		return uint(cols)
	}

	return uint(ws.col)
}

func (l *LineNoise) getCursorPosition(in io.Reader, out io.Writer) int {
	if n, err := out.Write([]byte("\x1b[6n")); n != 4 || err != nil {
		return -1
	}

	i := 0
	buf := make([]byte, 32)

	/* Read the response: ESC [ rows ; cols R */
	for i < len(buf)-1 {
		if n, _ := in.Read(buf[i : i+1]); n != 1 {
			break
		}
		if buf[i] == byte('R') {
			break
		}
		i++
	}

	/* Parse it. */
	if buf[0] != byte(ESC) || buf[1] != byte('[') {
		return -1
	}

	els := strings.Split(string(buf[2:i]), ";")
	cols, err := strconv.Atoi(els[1])
	if err != nil {
		return -1
	}
	return cols
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

// ClearScreen clears the screen.
func (l *LineNoise) ClearScreen() {
	fmt.Print("\033[H\033[2J")
}

func (l *LineNoise) Cleanup() {
	if l.rawMode {
		l.disableRawMode(os.Stdin.Fd())
	}
}

type Completion struct {
}

// Add adds the specified line to the completion list.
func (c *Completion) Add(line string) {

}

func isUnsupportedTerm() bool {
	term := os.Getenv("TERM")
	if term == "" {
		return true
	}

	for _, t := range unsupportedTerms {
		if term == t {
			return true
		}
	}
	return false
}
