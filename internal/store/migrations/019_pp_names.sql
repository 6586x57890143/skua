-- How each member was last seen called in the server, for the board.
alter table pp_member add column name text not null default '';
