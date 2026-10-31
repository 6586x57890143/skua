-- The deckhead: the luck feats each member has nailed a scrap up for at
-- the slack tide, a bit each.
alter table pp_member add column feats bigint not null default 0;
