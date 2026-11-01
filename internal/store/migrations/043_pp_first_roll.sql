-- The first roll: each member's place on the level ladder as the
-- preseason left it, for the first ten, 0 for none. Taken once, at the
-- turn into season 1.
alter table pp_member add column first_roll bigint not null default 0;
