The utility reads possibly continued lines from stdin, turns each continued
line into a single one that does not break.

Each line starting without white space starts a new line.
Lines after the first are considered continuations if they begin with a space or tab character.
