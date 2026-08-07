-- Project HANGAR — Phase 1b: mail.
-- 02_DATABASE_SCHEMA.md §5.2 "Mail (5)" and §5.2's "detail tables added for
-- parity" (`app.mail_body`, separated from `mail_header` because ESI itself
-- splits list metadata from the body fetch, and bodies are considerably
-- larger and rarer to page over). Character-scoped only.

-- +goose Up

-- #1
CREATE TABLE app.mail_header (
    character_id  bigint      NOT NULL REFERENCES app.character(character_id),
    mail_id       bigint      NOT NULL,
    from_id       bigint,
    subject       text,
    sent_at       timestamptz NOT NULL,   -- ESI field name is `timestamp`; reserved-word avoidance (Phase 1a precedent)
    is_read       boolean,
    labels        bigint[]    NOT NULL DEFAULT '{}',
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, mail_id)
);
CREATE INDEX ON app.mail_header (character_id, sent_at DESC);

-- #2
CREATE TABLE app.mail_body (
    character_id bigint      NOT NULL,
    mail_id      bigint      NOT NULL,
    body         text        NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, mail_id),
    FOREIGN KEY (character_id, mail_id) REFERENCES app.mail_header (character_id, mail_id) ON DELETE CASCADE
);

-- #3
CREATE TABLE app.mail_recipient (
    character_id  bigint  NOT NULL,
    mail_id       bigint  NOT NULL,
    recipient_id  bigint  NOT NULL,
    recipient_type text   NOT NULL,   -- open vocabulary: 'character'|'corporation'|'alliance'|'mailing_list'
    PRIMARY KEY (character_id, mail_id, recipient_id),
    FOREIGN KEY (character_id, mail_id) REFERENCES app.mail_header (character_id, mail_id) ON DELETE CASCADE
);

-- #4
CREATE TABLE app.mail_label (
    character_id bigint NOT NULL REFERENCES app.character(character_id),
    label_id     bigint NOT NULL,
    name         text   NOT NULL,
    color        text,
    unread_count integer,   -- NOT money
    PRIMARY KEY (character_id, label_id)
);

-- #5
CREATE TABLE app.mail_list (
    character_id bigint NOT NULL REFERENCES app.character(character_id),
    list_id      bigint NOT NULL,
    name         text   NOT NULL,
    PRIMARY KEY (character_id, list_id)
);

-- +goose Down
DROP TABLE app.mail_list;
DROP TABLE app.mail_label;
DROP TABLE app.mail_recipient;
DROP TABLE app.mail_body;
DROP TABLE app.mail_header;
