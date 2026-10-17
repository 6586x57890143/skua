-- /pp's beta keys. A key is kept only as its SHA-256, so the table can't
-- hand one out again. Each lets one member of one server into the beta,
-- once, until it expires (unix seconds).
create table pp_key (
	hash        bytea  primary key,
	guild_id    bigint not null,
	issued_by   bigint not null,
	expires     bigint not null,
	redeemed_by bigint not null default 0,
	redeemed_at bigint not null default 0
);
