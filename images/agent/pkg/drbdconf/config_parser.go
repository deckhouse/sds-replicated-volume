// Format description:
//   - https://linbit.com/man/v9/?linbitman=drbd.conf.5.html
//   - https://manpages.debian.org/bookworm/drbd-utils/drbd.conf-9.0.5.en.html
package drbdconf

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
)

func Parse(fsys fs.FS, name string) (*Root, error) {
	parser := &fileParser{}
	if err := parser.parseFile(fsys, name); err != nil {
		return nil, err
	}

	return parser.root, nil
}

type fileParser struct {
	included map[string]*Root
	fsys     fs.FS

	data []byte
	idx  int

	root *Root

	// for error reporting only, zero-based
	loc Location
}

// [Word] or [trivia]
type token interface {
	_token()
}

const TokenMaxLen = 255

type trivia byte

func (*trivia) _token() {}

const (
	triviaOpenBrace  trivia = '{'
	triviaCloseBrace trivia = '}'
	triviaSemicolon  trivia = ';'
)

func (p *fileParser) parseFile(fsys fs.FS, name string) (err error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fmt.Errorf("reading file %s: %w", name, err)
	}

	p.fsys = fsys
	p.data = data
	p.root = &Root{
		Filename: name,
	}
	if p.included == nil {
		p.included = map[string]*Root{}
	}
	p.included[name] = p.root

	// since comments are checked only on position advance,
	// we have to do an early check before the first advance happens
	p.skipComment()

	var words []Word

	for {
		var token token
		if token, err = p.parseToken(); err != nil {
			return p.report(err)
		}

		if token == nil { // EOF
			break
		}

		switch t := token.(type) {
		case *Word:
			words = append(words, *t)
		case *trivia:
			switch *t {
			case triviaOpenBrace:
				if len(words) == 0 {
					return p.report(errors.New("unexpected character '{'"))
				}
				s := &Section{
					Key:      words,
					Location: words[0].Location,
				}
				words = nil
				if s.Elements, err = p.parseSectionElements(); err != nil {
					return err
				}
				p.root.Elements = append(p.root.Elements, s)
			case triviaCloseBrace:
				return p.report(errors.New("unexpected character '}'"))
			case triviaSemicolon:
				if len(words) == 0 {
					return p.report(errors.New("unexpected character ';'"))
				}
				if words[0].Value != "include" {
					return p.report(errors.New("unrecognized keyword"))
				}
				if len(words) != 2 {
					return p.report(errors.New("expected exactly 1 argument in 'include'"))
				}

				incl := &Include{
					Glob: words[1].Value,
				}
				words = nil

				var inclNames []string

				if inclNames, err = fs.Glob(p.fsys, incl.Glob); err != nil {
					return p.report(fmt.Errorf("parsing glob pattern: %w", err))
				}

				for _, inclName := range inclNames {
					if !filepath.IsAbs(inclName) {
						// filepath is relative to current file
						inclName = filepath.Join(filepath.Dir(name), inclName)
					}

					inclRoot := p.included[inclName]
					if inclRoot == nil {
						includedParser := &fileParser{
							included: p.included,
						}
						if err := includedParser.parseFile(fsys, inclName); err != nil {
							return err
						}
						inclRoot = includedParser.root
					}

					incl.Files = append(incl.Files, inclRoot)
				}

				p.root.Elements = append(p.root.Elements, incl)
			default:
				panic("unexpected trivia type")
			}
		default:
			panic("unexpected token type")
		}
	}

	if len(words) > 0 {
		return fmt.Errorf("unexpected EOF")
	}

	return nil
}

// Returns:
//   - (slice of [Section] or [Parameter] elements, nil) in case of success
//   - (nil, [error]) in case of error
func (p *fileParser) parseSectionElements() (elements []SectionElement, err error) {
	p.skipWhitespace()

	var words []Word

	for {
		var token token
		if token, err = p.parseToken(); err != nil {
			return nil, err
		}

		if token == nil { // EOF
			return nil, p.report(errors.New("unexpected EOF"))
		}

		switch t := token.(type) {
		case *Word:
			words = append(words, *t)
		case *trivia:
			switch *t {
			case triviaOpenBrace:
				if len(words) == 0 {
					return nil, p.report(errors.New("unexpected character '{'"))
				}
				s := &Section{
					Key:      words,
					Location: words[0].Location,
				}
				words = nil
				if s.Elements, err = p.parseSectionElements(); err != nil {
					return nil, err
				}
				elements = append(elements, s)
			case triviaCloseBrace:
				if len(words) > 0 {
					return nil, p.report(errors.New("unexpected character '}'"))
				}
				return
			case triviaSemicolon:
				if len(words) == 0 {
					return nil, p.report(errors.New("unexpected character ';'"))
				}

				p := &Parameter{
					Key: words,
				}
				words = nil
				elements = append(elements, p)
			default:
				panic("unexpected trivia type")
			}
		default:
			panic("unexpected token type")
		}
	}
}

// Returns:
//   - ([trivia], nil) for trivia tokens.
//   - ([Word], nil) for word tokens.
//   - (nil, nil) in case of EOF.
//   - (nil, [error]) in case of error
func (p *fileParser) parseToken() (token, error) {
	p.skipWhitespace()
	if p.eof() {
		return nil, nil
	}

	if p.ch() == '"' {
		p.advance(false)
		return p.parseQuotedWord()
	}

	if tr, ok := newTrivia(p.ch()); ok {
		p.advance(true)
		return tr, nil
	}

	var word []byte
	loc := p.loc

	for ; !p.eof() && !isWordTerminatorChar(p.ch()); p.advance(true) {
		if !isTokenChar(p.ch()) {
			return nil, p.report(errors.New("unexpected char"))
		}
		if len(word) == TokenMaxLen {
			return nil, p.report(fmt.Errorf("token maximum length exceeded: %d", TokenMaxLen))
		}

		word = append(word, p.ch())
	}

	return &Word{Value: string(word), Location: loc}, nil
}

func (p *fileParser) parseQuotedWord() (*Word, error) {
	var word []byte
	loc := p.loc

	var escaping bool
	for ; ; p.advance(false) {
		if p.eof() {
			return nil, p.report(errors.New("unexpected EOF"))
		}

		if escaping {
			switch p.ch() {
			case '\\':
				word = append(word, '\\')
			case 'n':
				word = append(word, '\n')
			case '"':
				word = append(word, '"')
			default:
				return nil, p.report(errors.New("unexpected escape sequence"))
			}
			escaping = false
		} else {
			switch p.ch() {
			case '\\':
				escaping = true
			case '\n':
				return nil, p.report(errors.New("unexpected EOL"))
			case '"':
				// success
				p.advance(true)
				return &Word{
					IsQuoted: true,
					Value:    string(word),
					Location: loc,
				}, nil
			default:
				word = append(word, p.ch())
			}
		}
	}
}

func (p *fileParser) ch() byte {
	return p.data[p.idx]
}

func (p *fileParser) advance(skipComment bool) {
	p.advanceAndCountPosition()

	if skipComment {
		p.skipComment()
	}
}

func (p *fileParser) advanceAndCountPosition() {
	if p.ch() == '\n' {
		p.loc = p.loc.NextLine()
	} else {
		p.loc = p.loc.NextCol()
	}

	p.idx++
}

func (p *fileParser) eof() bool {
	return p.idx == len(p.data)
}

func (p *fileParser) skipComment() {
	if p.eof() || p.ch() != '#' {
		return
	}
	for !p.eof() && p.ch() != '\n' {
		p.advanceAndCountPosition()
	}
}

func (p *fileParser) skipWhitespace() {
	for !p.eof() && isWhitespace(p.ch()) {
		p.advance(true)
	}
}

func (p *fileParser) report(err error) error {
	return fmt.Errorf("%s: parsing error: %w %s", p.root.Filename, err, p.loc)
}

func newTrivia(ch byte) (*trivia, bool) {
	tr := trivia(ch)
	switch tr {
	case triviaCloseBrace:
		return &tr, true
	case triviaOpenBrace:
		return &tr, true
	case triviaSemicolon:
		return &tr, true
	default:
		return nil, false
	}
}

func isTokenChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') ||
		ch == '.' || ch == '/' || ch == '_' || ch == '-' || ch == ':'
}

func isWordTerminatorChar(ch byte) bool {
	return isWhitespace(ch) || ch == ';' || ch == '{'
}

func isWhitespace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n'
}
