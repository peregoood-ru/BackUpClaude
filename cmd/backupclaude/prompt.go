package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// prompter asks a person questions in the terminal. Every question has a
// default that a bare Enter accepts, and the end of the input accepts it too, so
// a script that feeds fewer lines than there are questions still finishes.
type prompter struct {
	reader *bufio.Reader
	out    io.Writer
	tr     *i18n.Translator
	// hidden reads a line without showing it; nil means reading it like any line.
	hidden func() (string, error)
}

func (a *app) newPrompter() *prompter {
	return &prompter{reader: a.input(), out: a.stdout, tr: a.tr, hidden: a.deps.readSecret}
}

// line reads one line without its line break. At the end of the input it
// returns an empty line.
func (p *prompter) line() (string, error) {
	text, err := p.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", i18n.E("setup.read_failed").Because(err)
	}
	return strings.TrimSpace(text), nil
}

// ask shows a question with its default and returns the answer.
func (p *prompter) ask(question, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(p.out, "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(p.out, "%s: ", question)
	}
	answer, err := p.line()
	if err != nil {
		return "", err
	}
	if answer == "" {
		return def, nil
	}
	return answer, nil
}

// yesNo asks a question that is answered with yes or no.
func (p *prompter) yesNo(question string, def bool) (bool, error) {
	hint := p.tr.T("setup.yes_no.no_default")
	if def {
		hint = p.tr.T("setup.yes_no.yes_default")
	}
	yes, no := strings.Fields(p.tr.T("setup.yes_words")), strings.Fields(p.tr.T("setup.no_words"))
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprintf(p.out, "%s [%s]: ", question, hint)
		answer, err := p.line()
		if err != nil {
			return false, err
		}
		answer = strings.ToLower(answer)
		switch {
		case answer == "":
			return def, nil
		case inList(yes, answer):
			return true, nil
		case inList(no, answer):
			return false, nil
		}
		fmt.Fprintln(p.out, p.tr.T("setup.yes_no.repeat", strings.Join(yes, "/"), strings.Join(no, "/")))
	}
	return def, nil
}

// secret asks for something that must not appear on the screen.
func (p *prompter) secret(question string) (string, error) {
	fmt.Fprintf(p.out, "%s: ", question)
	if p.hidden == nil {
		return p.line()
	}
	text, err := p.hidden()
	fmt.Fprintln(p.out)
	if err != nil {
		return "", i18n.E("setup.read_failed").Because(err)
	}
	return strings.TrimSpace(text), nil
}

// choose shows numbered options and returns the index of the chosen one.
func (p *prompter) choose(question string, options []string, def int) (int, error) {
	fmt.Fprintln(p.out, question)
	for i, option := range options {
		fmt.Fprintf(p.out, "  %d. %s\n", i+1, option)
	}
	for attempt := 0; attempt < 3; attempt++ {
		answer, err := p.ask(p.tr.T("setup.choose.prompt"), fmt.Sprint(def+1))
		if err != nil {
			return def, err
		}
		var n int
		if _, scanErr := fmt.Sscanf(answer, "%d", &n); scanErr == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		fmt.Fprintln(p.out, p.tr.T("setup.choose.repeat", len(options)))
	}
	return def, nil
}

func inList(list []string, item string) bool {
	for _, candidate := range list {
		if candidate == item {
			return true
		}
	}
	return false
}
