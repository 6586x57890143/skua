-- Whether a member's /pp menu is icons only (compact) or icons with
-- their names.
alter table pp_member add column compact boolean not null default false;
